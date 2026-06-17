package orchestrator

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/dbflow-validator/dbflow-validator/internal/config"
	"github.com/dbflow-validator/dbflow-validator/internal/container"
	"github.com/dbflow-validator/dbflow-validator/internal/domain"
	"github.com/dbflow-validator/dbflow-validator/internal/liquibase"
	"github.com/dbflow-validator/dbflow-validator/internal/maven"
)

// Deps holds all port implementations wired at application startup.
type Deps struct {
	Preflight  domain.PreflightChecker
	Cloner     domain.Cloner
	DBProvider domain.DatabaseProvider
	Patcher    domain.PropertiesPatcher
	Engine     domain.EngineDetector
	Tags       domain.TagResolver
	Maven      domain.MavenRunner
	// ReadinessPolicy overrides the default retry policy for the readiness probe.
	// Leave nil to use container.DefaultRetryPolicy.
	ReadinessPolicy *container.RetryPolicy
}

// Run executes the full linear validation flow:
//  1. Preflight host-binary checks
//  2. Git clone into 0700 temp dir
//  3. Engine guard (detect Postgres or abort)
//  4. Start ephemeral Postgres container
//  5. Readiness probe (active SELECT 1 retry loop)
//  6. Patch liquibase.properties with ephemeral coords
//  7. Run dbflow:sync
//  8. Resolve first-tag from master-changelog
//  9. Run dbflow:rollback
//  10. Report
//
// Cleanup (container stop + temp dir removal) is registered eagerly and runs
// on ALL exit paths via deferred registry.RunAll().
func Run(ctx context.Context, deps Deps, cfg config.Config) domain.RunReport {
	started := time.Now()
	reg := NewCleanupRegistry()
	defer func() {
		errs := reg.RunAll()
		for _, e := range errs {
			slog.Warn("cleanup error", "err", e)
		}
	}()

	var steps []domain.StepResult
	overallStatus := domain.StatusPassed

	fail := func(name string, msg string, err error) domain.RunReport {
		if err != nil {
			msg = fmt.Sprintf("%s: %v", msg, err)
		}
		steps = append(steps, domain.StepResult{
			Name:   name,
			Status: domain.StepStatusFailed,
			Error:  msg,
		})
		overallStatus = domain.StatusFailed
		return buildReport(started, cfg, overallStatus, steps)
	}

	pass := func(name string, duration time.Duration) {
		steps = append(steps, domain.StepResult{
			Name:       name,
			Status:     domain.StepStatusPassed,
			Duration:   duration,
			DurationMs: duration.Milliseconds(),
		})
	}

	// --- Step 1: Preflight ---
	t0 := time.Now()
	if _, err := deps.Preflight.Check(ctx); err != nil {
		return fail("preflight", "pre-flight host checks failed", err)
	}
	pass("preflight", time.Since(t0))

	// --- Step 2: Clone ---
	t0 = time.Now()
	cloneRoot, err := deps.Cloner.Clone(ctx, domain.CloneOptions{
		RepoURL: cfg.RepoURL,
		Branch:  cfg.BaseBranch,
		Token:   cfg.Token,
		DestDir: tempCloneDir(),
	})
	if err != nil {
		return fail("clone", "git clone failed", err)
	}
	// Register cleanup for temp clone dir.
	reg.Register(func() error {
		return os.RemoveAll(cloneRoot)
	})
	pass("clone", time.Since(t0))

	// --- Step 3: Engine guard ---
	t0 = time.Now()
	propsPath := cloneRoot + "/src/main/resources/db/liquibase.properties"
	_, err = deps.Engine.Detect(propsPath)
	if err != nil {
		return fail("engine-guard", "engine detection failed", err)
	}
	pass("engine-guard", time.Since(t0))

	// --- Step 4: Start container ---
	t0 = time.Now()
	containerProvider := deps.DBProvider.ContainerProvider()
	coords, err := containerProvider.Start(ctx)
	if err != nil {
		return fail("container-start", "failed to start ephemeral Postgres container", err)
	}
	// Register cleanup for the container.
	reg.Register(func() error {
		return containerProvider.Stop(context.Background())
	})
	pass("container-start", time.Since(t0))

	// --- Step 5: Readiness probe ---
	t0 = time.Now()
	dsn := deps.DBProvider.DSN(coords)
	pingFn := func(pctx context.Context) error {
		return deps.DBProvider.Ping(pctx, dsn)
	}
	policy := container.DefaultRetryPolicy
	if deps.ReadinessPolicy != nil {
		policy = *deps.ReadinessPolicy
	}
	readinessErr := container.WaitReady(
		ctx,
		pingFn,
		policy,
		time.Now,
		func(d time.Duration) { time.Sleep(d) },
	)
	if readinessErr != nil {
		return fail("readiness-probe", "database readiness probe timed out", readinessErr)
	}
	pass("readiness-probe", time.Since(t0))

	// --- Step 6a: Schema extraction + lb_<schema> user + GRANT-target roles ---
	// Extract the target schema name from the archetype DDL to derive the
	// ephemeral connection user (lb_<schema>) and auto-create GRANT-target roles.
	t0 = time.Now()
	adminDSN := deps.DBProvider.DSN(coords)

	schemaName, schemaErr := liquibase.ExtractSchemaFromArchetype(cloneRoot)
	if schemaErr != nil {
		slog.Warn("cannot extract schema name from archetype; using default admin user",
			"err", schemaErr)
	}

	lbUsername := coords.User // default: throwaway admin user
	lbPassword := coords.Password

	if schemaName != "" {
		lbUsername = liquibase.LbUsername(schemaName)
		lbPassword = "lb_v4lid4t0r_pass" // throwaway password for lb user

		// Create the lb_<schema> login role.
		if err := container.CreateLbUser(ctx, adminDSN, lbUsername, lbPassword); err != nil {
			return fail("schema-setup", "failed to create lb_<schema> user", err)
		}

		// Auto-create any GRANT-target roles found in the archetype DDL.
		grantRoles, _ := liquibase.ExtractGrantTargetRolesFromArchetype(cloneRoot)
		if len(grantRoles) > 0 {
			if err := container.CreateRolesIfNotExist(ctx, adminDSN, grantRoles); err != nil {
				return fail("schema-setup", "failed to create GRANT-target roles", err)
			}
		}

		// Grant the lb user CONNECT on the throwaway database.
		if err := grantLbConnect(ctx, adminDSN, lbUsername, coords.DBName); err != nil {
			slog.Warn("could not grant CONNECT to lb user; sync may fail with auth error",
				"user", lbUsername, "err", err)
		}
	}
	pass("schema-setup", time.Since(t0))

	// Build lb_coords with the lb_<schema> user for liquibase.properties patching.
	lbCoords := domain.ContainerCoords{
		Host:     coords.Host,
		Port:     coords.Port,
		User:     lbUsername,
		Password: lbPassword,
		DBName:   coords.DBName,
	}

	// --- Step 6b: Inject PostgreSQL driver into cloned pom.xml ---
	// The relational-db-release-manager-plugin is a shaded jar that bundles
	// Oracle/MySQL/Snowflake drivers but NOT PostgreSQL. The plugin classloader
	// is isolated; the driver must be declared inside <plugin><dependencies>.
	t0 = time.Now()
	clonedPomPath := cloneRoot + "/pom.xml"
	if err := maven.InjectDriverDependency(clonedPomPath); err != nil {
		return fail("pom-driver-inject", "failed to inject PostgreSQL driver into cloned pom.xml", err)
	}
	pass("pom-driver-inject", time.Since(t0))

	// --- Step 6c: Patch liquibase.properties ---
	t0 = time.Now()
	if err := deps.Patcher.Patch(propsPath, lbCoords); err != nil {
		return fail("properties-patch", "failed to patch liquibase.properties", err)
	}
	pass("properties-patch", time.Since(t0))

	// --- Step 7: dbflow:sync ---
	t0 = time.Now()
	var syncOutput bytes.Buffer
	syncResult, err := deps.Maven.Run(ctx, cloneRoot, maven.GoalSync, syncParams(), &syncOutput)
	if err != nil {
		return fail(maven.GoalSync, "maven runner error during sync", err)
	}
	syncResult.Duration = time.Since(t0)
	syncResult.DurationMs = syncResult.Duration.Milliseconds()
	steps = append(steps, syncResult)
	if syncResult.Status != domain.StepStatusPassed {
		overallStatus = domain.StatusFailed
		return buildReport(started, cfg, overallStatus, steps)
	}

	// --- Step 8: First-tag resolution ---
	t0 = time.Now()
	firstTag, err := deps.Tags.FirstTag(cloneRoot)
	if err != nil {
		return fail("first-tag", "first-tag resolution failed", err)
	}
	pass("first-tag", time.Since(t0))

	// --- Step 9: dbflow:rollback ---
	t0 = time.Now()
	var rollbackOutput bytes.Buffer
	rollbackResult, err := deps.Maven.Run(ctx, cloneRoot, maven.GoalRollback, rollbackParams(firstTag), &rollbackOutput)
	if err != nil {
		return fail(maven.GoalRollback, "maven runner error during rollback", err)
	}
	rollbackResult.Duration = time.Since(t0)
	rollbackResult.DurationMs = rollbackResult.Duration.Milliseconds()
	steps = append(steps, rollbackResult)
	if rollbackResult.Status != domain.StepStatusPassed {
		overallStatus = domain.StatusFailed
	}

	return buildReport(started, cfg, overallStatus, steps)
}

// grantLbConnect grants CONNECT privilege on the given database to lbUsername.
// This is required so the lb_<schema> user (which is not the Postgres super-user)
// can connect to the throwaway database.
func grantLbConnect(ctx context.Context, adminDSN, lbUsername, dbName string) error {
	return container.ExecSQL(ctx, adminDSN,
		fmt.Sprintf("GRANT CONNECT ON DATABASE %s TO %s", dbName, lbUsername),
	)
}

// syncParams returns the KV pairs for dbflow:sync.
// TAG is injected automatically by the Maven runner (unique per run).
// AUTHOR identifies the caller.
func syncParams() []string {
	return []string{"--AUTHOR=validator-cli"}
}

// rollbackParams returns the KV pairs for dbflow:rollback.
func rollbackParams(tag string) []string {
	return []string{fmt.Sprintf("--TAG=%s", tag)}
}

func buildReport(started time.Time, cfg config.Config, status domain.Status, steps []domain.StepResult) domain.RunReport {
	ended := time.Now()
	return domain.RunReport{
		Status:     status,
		Timestamp:  started,
		RepoURL:    cfg.RepoURL,
		BaseBranch: cfg.BaseBranch,
		Steps:      steps,
		TotalDurMs: ended.Sub(started).Milliseconds(),
		Started:    started,
		Ended:      ended,
	}
}

func tempCloneDir() string {
	dir, err := os.MkdirTemp("", "dbflow-clone-*")
	if err != nil {
		// MkdirTemp should never fail in normal operation; panic is appropriate.
		panic(fmt.Sprintf("cannot create temp clone dir: %v", err))
	}
	// chmod 0700 (MkdirTemp uses 0700 on Linux already, but be explicit).
	_ = os.Chmod(dir, 0o700)
	return dir
}
