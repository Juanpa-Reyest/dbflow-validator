package orchestrator

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dbflow-validator/dbflow-validator/internal/config"
	"github.com/dbflow-validator/dbflow-validator/internal/container"
	"github.com/dbflow-validator/dbflow-validator/internal/domain"
	"github.com/dbflow-validator/dbflow-validator/internal/liquibase"
	"github.com/dbflow-validator/dbflow-validator/internal/maven"
	internalvendor "github.com/dbflow-validator/dbflow-validator/internal/vendor"
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
	// NetworkCleanup is the cleanup function returned by container.NewNetwork.
	// When non-nil, it is registered FIRST in the LIFO CleanupRegistry so the
	// Docker network is torn down LAST (after both containers are stopped).
	// Leave nil when no Docker network is in use (e.g. unit tests).
	NetworkCleanup func() error
	// MavenRepoCachePath is the host path to the vendored Maven repository (mvn-vendor/repository).
	// When non-empty, the orchestrator writes settings.xml into the clone dir so the
	// ContainerRunner can pick it up at /work/settings.xml inside the Maven container.
	// Leave empty to use the host's default Maven settings (not recommended for production).
	MavenRepoCachePath string
	// MavenOut is the io.Writer that receives Maven container stdout/stderr.
	// When non-nil it is passed directly to deps.Maven.Run so Maven output flows to
	// both the live console and execution.log (dual-sink). When nil, io.Discard is used.
	MavenOut io.Writer
	// RunDir is the per-run artifacts directory (<output-dir>/<timestamp>/).
	// Set by main.go after resolving rundir.RunDirPath. Empty string means
	// run-dir creation failed (degraded mode) — no file artifacts written.
	RunDir string
	// KeepWorkspace, when true, retains the ephemeral clone under <run>/workspace/
	// even on a PASSED run. Mirrors cfg.KeepWorkspace but is threaded via Deps to
	// keep the cleanup logic inside the orchestrator.
	KeepWorkspace bool
	// ReadinessPolicy overrides the default retry policy for the readiness probe.
	// Leave nil to use container.DefaultRetryPolicy.
	ReadinessPolicy *container.RetryPolicy
	// PreSyncValidator is an optional extensibility seam for plugging in a SQL-rules
	// validation step before dbflow:sync. Leave nil to use the no-op default.
	// When set, ValidatePreSync is called after properties-patch and before sync.
	// A non-nil error aborts the pipeline with step name "pre-sync-validate".
	PreSyncValidator domain.PreSyncValidator
	// Overlayer copies the developer's local SQLInput tree into the cloned repo's
	// src/main/resources/SQLInput/ directory before sync. Leave nil to skip the
	// overlay step (backward-compatible with tests that do not wire an overlayer).
	Overlayer domain.Overlayer
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

	// Register Docker network cleanup FIRST (LIFO = runs LAST).
	// The network must be torn down after both the Postgres and Maven containers
	// are stopped; registering first ensures it is the last item executed.
	if deps.NetworkCleanup != nil {
		reg.Register(deps.NetworkCleanup)
	}

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

	// failUsage signals a configuration/usage error (exit code 2).
	// Use this for pre-clone guards like missing or empty SQLInput.
	failUsage := func(name string, msg string, err error) domain.RunReport {
		if err != nil {
			msg = fmt.Sprintf("%s: %v", msg, err)
		}
		steps = append(steps, domain.StepResult{
			Name:   name,
			Status: domain.StepStatusFailed,
			Error:  msg,
		})
		overallStatus = domain.StatusUsageError
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

	// --- Step 0: Input check (fail-fast guard) ---
	// Validate the local SQLInput directory BEFORE any clone, container, or Maven
	// operation. If the path is missing or contains no .sql files, abort here.
	// This avoids a cryptic Maven "SQLInput vacía" BUILD FAILURE downstream.
	// Uses StatusUsageError so main.go maps it to exit code 2.
	if cfg.SQLInputPath != "" {
		if n, err := countSQLFilesInDir(cfg.SQLInputPath); err != nil || n == 0 {
			msg := fmt.Sprintf("no pending SQL found in %s — nothing to validate", cfg.SQLInputPath)
			return failUsage("input-check", msg, domain.ErrNoPendingSQL)
		}
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
	// Register status-conditional cleanup for the ephemeral clone directory.
	//
	// The closure captures &overallStatus and reads it at run time (inside deferred
	// reg.RunAll()), which executes AFTER Run() has set overallStatus to its terminal
	// value. This is the core of the status-conditional workspace retention:
	//
	//   - PASSED and !KeepWorkspace → os.RemoveAll(cloneRoot) — normal success cleanup
	//   - Any other outcome (FAILED, ABORTED, USAGE_ERROR) OR KeepWorkspace=true →
	//     MoveWorkspace(cloneRoot, <runDir>/workspace/) — retain the clone for debugging
	//
	// When deps.RunDir is empty (run-dir creation failed / degraded mode), fall back
	// to unconditional removal to avoid leaking temp dirs.
	finalStatus := &overallStatus
	runDir := deps.RunDir
	keepWorkspace := deps.KeepWorkspace
	reg.Register(func() error {
		// Remove on success (unless keep-workspace requested) or when runDir is empty.
		if *finalStatus == domain.StatusPassed && !keepWorkspace {
			return os.RemoveAll(cloneRoot)
		}
		if runDir == "" {
			// Degraded mode: run-dir not available, fall back to removal.
			slog.Warn("run dir unavailable; removing clone dir (cannot retain workspace)", "cloneRoot", cloneRoot)
			return os.RemoveAll(cloneRoot)
		}
		// Move the clone into <runDir>/workspace/ for post-mortem inspection.
		return MoveWorkspace(cloneRoot, filepath.Join(runDir, "workspace"))
	})
	pass("clone", time.Since(t0))

	// Write settings.xml into the clone dir so the Maven container can pick it up
	// at /work/settings.xml. This is required when MavenRepoCachePath is set and
	// a ContainerRunner is in use. When MavenRepoCachePath is empty, settings.xml
	// is not written (host Maven or pre-configured runner used instead).
	if deps.MavenRepoCachePath != "" {
		if _, err := internalvendor.WriteSettingsXML(cloneRoot, deps.MavenRepoCachePath); err != nil {
			slog.Warn("could not write settings.xml into clone dir; Maven may fail to resolve offline artifacts",
				"err", err)
		}
	}

	// --- Step 3: Engine guard ---
	t0 = time.Now()
	propsPath := cloneRoot + "/src/main/resources/db/liquibase.properties"
	_, err = deps.Engine.Detect(propsPath)
	if err != nil {
		return fail("engine-guard", "engine detection failed", err)
	}
	pass("engine-guard", time.Since(t0))

	// --- Step 3b: SQLInput overlay ---
	// Copy the developer's local SQLInput tree into the clone's
	// src/main/resources/SQLInput/ directory before starting any container or Maven.
	// When deps.Overlayer is nil (legacy / test without overlay), skip this step.
	if deps.Overlayer != nil {
		t0 = time.Now()
		destSQLInput := cloneRoot + "/src/main/resources/SQLInput"
		if _, err := deps.Overlayer.Apply(cfg.SQLInputPath, destSQLInput); err != nil {
			return fail("overlay", "SQLInput overlay failed", err)
		}
		pass("overlay", time.Since(t0))
	}

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

		// Grant the lb user CONNECT and CREATE on the throwaway database.
		// CONNECT is required to establish sessions; CREATE is required so the lb
		// user can create the application schema (scgolfcore, etc.) inside the DB.
		// Mirrors ambientacion.sql: GRANT CONNECT, CREATE ON DATABASE dbtest TO scliquibase.
		if err := container.GrantConnectCreateOnDatabase(ctx, adminDSN, coords.DBName, lbUsername); err != nil {
			slog.Warn("could not grant CONNECT, CREATE on database to lb user; sync may fail",
				"user", lbUsername, "db", coords.DBName, "err", err)
		}

		// Create the lb_<schema> bookkeeping schema owned by the lb user.
		// Liquibase resolves DATABASECHANGELOG via search_path "$user" — it looks for
		// the schema matching the connection username. Without this schema, Liquibase
		// falls back to public and may fail with permission errors.
		// Mirrors ambientacion.sql: CREATE SCHEMA scliquibase; ALTER SCHEMA scliquibase OWNER TO scliquibase.
		if err := container.CreateLbBookkeepingSchema(ctx, adminDSN, lbUsername); err != nil {
			slog.Warn("could not create lb bookkeeping schema; sync may fail with schema errors",
				"schema", lbUsername, "err", err)
		}
	}
	pass("schema-setup", time.Since(t0))

	// Build lb_coords with the lb_<schema> user for liquibase.properties patching.
	//
	// DUAL-COORDINATES: the admin path (Host:Port) is used above for provisioning;
	// the patch path must use AliasHost:AliasPort so the JDBC URL in
	// liquibase.properties resolves inside the Maven container's Docker network.
	// Both coords.Host and coords.AliasHost are forwarded; patch.go selects the
	// alias when AliasHost is non-empty.
	lbCoords := domain.ContainerCoords{
		Host:      coords.Host,
		Port:      coords.Port,
		AliasHost: coords.AliasHost,
		AliasPort: coords.AliasPort,
		User:      lbUsername,
		Password:  lbPassword,
		DBName:    coords.DBName,
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

	// --- Step 6d: Pre-sync validation (optional seam) ---
	// When a PreSyncValidator is wired, it runs here — before the ephemeral sync —
	// mirroring the real pipeline's validate → validate-ephemeral order.
	// A nil validator is treated as the no-op default (always passes).
	t0 = time.Now()
	preSyncValidator := deps.PreSyncValidator
	if preSyncValidator == nil {
		preSyncValidator = domain.NoOpPreSyncValidator{}
	}
	if err := preSyncValidator.ValidatePreSync(ctx, cloneRoot); err != nil {
		return fail("pre-sync-validate", "pre-sync validation failed", err)
	}
	pass("pre-sync-validate", time.Since(t0))

	// Resolve the Maven output writer: use MavenOut if provided, otherwise discard.
	// MavenOut is wired in main.go to logging.MavenWriter(os.Stderr, logFile) so
	// Maven stdout/stderr flows to both the live console and execution.log.
	mavenOut := deps.MavenOut
	if mavenOut == nil {
		mavenOut = io.Discard
	}

	// --- Step 7: dbflow:sync ---
	t0 = time.Now()
	syncResult, err := deps.Maven.Run(ctx, cloneRoot, maven.GoalSync, syncParams(), mavenOut)
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
	rollbackResult, err := deps.Maven.Run(ctx, cloneRoot, maven.GoalRollback, rollbackParams(firstTag), mavenOut)
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

// countSQLFilesInDir returns the count of regular .sql files in dir (recursive).
// Returns (0, nil) if dir does not exist — callers treat that as "no SQL found".
func countSQLFilesInDir(dir string) (int, error) {
	count := 0
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if !d.IsDir() && d.Type().IsRegular() && strings.EqualFold(filepath.Ext(d.Name()), ".sql") {
			count++
		}
		return nil
	})
	if os.IsNotExist(err) {
		return 0, nil
	}
	return count, err
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
