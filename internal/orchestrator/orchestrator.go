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

// redactURL removes any embedded userinfo (credentials) from a URL string so it
// is safe to include in trace output.  Returns the original string unchanged when
// it is not a valid URL or contains no userinfo.
func redactURL(raw string) string {
	// Simple token-based redact: strip everything between "://" and the first "@".
	if i := strings.Index(raw, "://"); i >= 0 {
		rest := raw[i+3:]
		if j := strings.Index(rest, "@"); j >= 0 {
			return raw[:i+3] + "<redacted>@" + rest[j+1:]
		}
	}
	return raw
}

// StepEvent carries progress information for a single orchestration step.
// It is delivered to Deps.OnStep both when a step starts (Done=false) and
// when it completes (Done=true, Failed reflects the outcome).
type StepEvent struct {
	// Name is the step identifier (e.g. "preflight", "clone", "dbflow:sync").
	Name string
	// Done is false when the step is starting, true when it has finished.
	Done bool
	// Failed is true when Done=true and the step did not pass.
	// Always false when Done=false.
	Failed bool
}

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
	// NetworkName is the Docker network name used to connect Postgres and Maven
	// containers. Included in trace logs so engineers can correlate container IDs
	// with the network. Leave empty when no Docker network is in use.
	NetworkName string
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
	// Logger is the structured logger used for trace output (step boundaries, timing,
	// resolved identifiers). When nil, slog.Default() is used. Inject a capturing
	// logger in tests to assert trace events without touching the global logger.
	Logger *slog.Logger
	// OnStep, when non-nil, is called twice for each step: once when the step
	// starts (Done=false) and once when it completes (Done=true). Use this to
	// emit clean per-step progress lines to the console without mixing slog output.
	OnStep func(StepEvent)
}

// logger resolves the logger to use: the injected one from Deps.Logger, or slog.Default.
// This allows tests to inject a capturing logger without touching the global default.
func logger(deps Deps) *slog.Logger {
	if deps.Logger != nil {
		return deps.Logger
	}
	return slog.Default()
}

// notify dispatches a StepEvent to deps.OnStep when it is non-nil.
// It is a no-op when OnStep is nil, preserving backward compatibility.
func notify(deps Deps, name string, done, failed bool) {
	if deps.OnStep != nil {
		deps.OnStep(StepEvent{Name: name, Done: done, Failed: failed})
	}
}

// cleanupState tracks workspace and resource state needed to build the cleanup
// StepResult trace. Fields are set incrementally as the run progresses; the
// runCleanupStep helper reads them after reg.RunAll() completes.
type cleanupState struct {
	// cloneRoot is the ephemeral clone directory path. Empty when clone has not
	// yet been registered (pre-clone early exits).
	cloneRoot string
	// runDir mirrors deps.RunDir; used to show the retention path in the trace.
	runDir string
	// keepWorkspace mirrors deps.KeepWorkspace.
	keepWorkspace bool
	// finalStatus points to the overallStatus variable inside Run so that the
	// cleanup function can read the terminal value at call time.
	finalStatus *domain.Status
	// networkName is the Docker network name for the trace label.
	networkName string
	// containerRegistered is true when the container Stop closure has been
	// registered with the CleanupRegistry. False on early exits (before
	// container-start) — the trace shows n/a rather than "eliminado".
	containerRegistered bool
	// networkRegistered is true when the network cleanup closure has been
	// registered with the CleanupRegistry. False when deps.NetworkCleanup is nil
	// or when the run exits before the network registration step.
	networkRegistered bool
}

// runCleanupStep executes all registered cleanup functions eagerly, then builds
// and returns a domain.StepResult that describes what was torn down.
//
// The returned step is always status PASSED unless one or more cleanup functions
// returned an error, in which case its status is FAILED and Error is populated.
// In either case the step does NOT alter the overall validation verdict —
// callers must treat cleanup errors as warning-level.
//
// The registry is idempotent: the deferred RunAll call in Run will be a no-op.
func runCleanupStep(reg *CleanupRegistry, cs cleanupState, started time.Time) domain.StepResult {
	t0 := time.Now()
	errs := reg.RunAll()

	status := domain.StepStatusPassed
	errMsg := ""
	if len(errs) > 0 {
		status = domain.StepStatusFailed
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		errMsg = strings.Join(msgs, "; ")
	}

	trace := buildCleanupTrace(cs, errs)

	return domain.StepResult{
		Name:       "cleanup",
		Status:     status,
		Duration:   time.Since(t0),
		DurationMs: time.Since(t0).Milliseconds(),
		Trace:      trace,
		Error:      errMsg,
	}
}

// buildCleanupTrace builds the human-readable trace for the cleanup step.
// It describes what was torn down and what was retained (workspace on FAILED runs).
// Resources that were never created (early-failure runs) are reported as n/a.
func buildCleanupTrace(cs cleanupState, errs []error) string {
	var lines []string

	// Container line — show "eliminado" only when the container was actually started
	// and its Stop closure registered. On early failures (before container-start)
	// show n/a to avoid misleading "eliminado" for a container that never existed.
	if cs.containerRegistered {
		lines = append(lines, "contenedor postgres    eliminado")
	} else {
		lines = append(lines, "contenedor postgres    n/a  (no se creó)")
	}

	// Network line — same conditional logic as the container.
	if cs.networkRegistered {
		lines = append(lines, "red docker             eliminada")
	} else {
		lines = append(lines, "red docker             n/a  (no se creó)")
	}

	// Workspace line depends on the terminal status and keepWorkspace flag.
	if cs.cloneRoot == "" {
		// Clone never ran (very early failure) — no workspace to report.
		lines = append(lines, "workspace (clon)       n/a  (no clone registered)")
	} else if cs.finalStatus != nil && *cs.finalStatus == domain.StatusPassed && !cs.keepWorkspace {
		lines = append(lines, "workspace (clon)       eliminado")
	} else {
		// Retained: FAILED, ABORTED, USAGE_ERROR, or KeepWorkspace=true.
		retainPath := strings.TrimRight(cs.runDir, "/") + "/workspace/"
		if cs.runDir == "" {
			retainPath = "(run dir unavailable — clone removed as fallback)"
		}
		lines = append(lines, fmt.Sprintf("workspace (clon)       RETENIDO → %s", retainPath))
	}

	// Teardown errors summary (if any).
	if len(errs) > 0 {
		lines = append(lines, "")
		lines = append(lines, "ADVERTENCIA — teardown errors:")
		for _, e := range errs {
			lines = append(lines, "  "+e.Error())
		}
	}

	return strings.Join(lines, "\n")
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
//  10. Cleanup (eager run before report) — captured as a visible step
//  11. Report
//
// Cleanup (container stop + temp dir removal) is registered eagerly and runs
// on ALL exit paths. The cleanup step is appended to steps BEFORE the report
// is built so it appears in the summary table and DETALLE blocks.
// The deferred RunAll() is kept as a SIGINT-safety net; it is a no-op when
// cleanup has already run eagerly.
func Run(ctx context.Context, deps Deps, cfg config.Config) domain.RunReport {
	log := logger(deps)
	started := time.Now()
	reg := NewCleanupRegistry()

	// Safety net: if the process receives SIGINT/SIGTERM before the eager cleanup
	// runs (or if Run panics), the deferred registry still executes cleanup.
	// RunAll is idempotent — the deferred call is a no-op when eager cleanup
	// already ran. Cleanup errors from the safety-net path are logged only
	// (the structured cleanup step may not be rendered in that case, which is
	// acceptable per the design spec).
	defer func() {
		errs := reg.RunAll()
		for _, e := range errs {
			log.Warn("cleanup error (safety-net defer)", "err", e)
		}
	}()

	// Emit the run preamble at Info so it appears on the console at the default log level.
	log.Info("run started",
		"repo_url", cfg.RepoURL,
		"base_branch", cfg.BaseBranch,
		"sql_input", cfg.SQLInputPath,
		"network", deps.NetworkName,
	)

	var steps []domain.StepResult
	overallStatus := domain.StatusPassed

	// cs accumulates workspace/resource metadata for the cleanup step trace.
	// Fields are filled in as the run progresses. networkRegistered is set here
	// (before network registration) so it is available for the trace builder.
	cs := cleanupState{
		runDir:        deps.RunDir,
		keepWorkspace: deps.KeepWorkspace,
		finalStatus:   &overallStatus,
		networkName:   deps.NetworkName,
	}

	// Register Docker network cleanup FIRST (LIFO = runs LAST).
	// The network must be torn down after both the Postgres and Maven containers
	// are stopped; registering first ensures it is the last item executed.
	if deps.NetworkCleanup != nil {
		reg.Register(deps.NetworkCleanup)
		cs.networkRegistered = true
	}

	// appendCleanupAndBuild runs cleanup eagerly, appends the cleanup StepResult
	// to steps, notifies OnStep, and returns the final RunReport.
	// All exit paths (success, failure, early exit) must call this instead of
	// buildReport directly to guarantee the cleanup step is always included.
	appendCleanupAndBuild := func() domain.RunReport {
		notify(deps, "cleanup", false, false)
		cleanupStep := runCleanupStep(reg, cs, started)
		notify(deps, "cleanup", true, cleanupStep.Status != domain.StepStatusPassed)
		steps = append(steps, cleanupStep)
		return buildReport(started, cfg, overallStatus, steps)
	}

	fail := func(name string, msg string, err error) domain.RunReport {
		if err != nil {
			msg = fmt.Sprintf("%s: %v", msg, err)
		}
		log.Error("step failed", "step", name, "error", msg)
		notify(deps, name, true, true)
		steps = append(steps, domain.StepResult{
			Name:   name,
			Status: domain.StepStatusFailed,
			Error:  msg,
		})
		overallStatus = domain.StatusFailed
		return appendCleanupAndBuild()
	}

	// failUsage signals a configuration/usage error (exit code 2).
	// Use this for pre-clone guards like missing or empty SQLInput.
	failUsage := func(name string, msg string, err error) domain.RunReport {
		if err != nil {
			msg = fmt.Sprintf("%s: %v", msg, err)
		}
		log.Warn("step usage-error", "step", name, "error", msg)
		notify(deps, name, true, true)
		steps = append(steps, domain.StepResult{
			Name:   name,
			Status: domain.StepStatusFailed,
			Error:  msg,
		})
		overallStatus = domain.StatusUsageError
		return appendCleanupAndBuild()
	}

	// passWithTrace records a passed step and populates StepResult.Trace with a
	// multi-line summary of what the step did.  Use this for all non-Maven steps so
	// every DETALLE block in execution.log carries meaningful content.
	passWithTrace := func(name string, duration time.Duration, trace string) {
		log.Debug("step.done", "step", name, "duration_ms", duration.Milliseconds())
		notify(deps, name, true, false)
		steps = append(steps, domain.StepResult{
			Name:       name,
			Status:     domain.StepStatusPassed,
			Duration:   duration,
			DurationMs: duration.Milliseconds(),
			Trace:      strings.TrimRight(trace, "\n"),
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
	log.Info("step.start", "step", "preflight")
	notify(deps, "preflight", false, false)
	t0 := time.Now()
	toolStatuses, err := deps.Preflight.Check(ctx)
	if err != nil {
		return fail("preflight", "pre-flight host checks failed", err)
	}
	{
		var lines []string
		for _, ts := range toolStatuses {
			lines = append(lines, fmt.Sprintf("%-10s found at %s", ts.Name, ts.Path))
		}
		passWithTrace("preflight", time.Since(t0), strings.Join(lines, "\n"))
	}

	// --- Step 2: Clone ---
	log.Info("step.start", "step", "clone",
		"repo_url", cfg.RepoURL,
		"branch", cfg.BaseBranch,
	)
	notify(deps, "clone", false, false)
	t0 = time.Now()
	// Create the temp dir BEFORE cloning and register its unconditional removal
	// immediately. This guarantees the dir is always torn down even when Clone
	// returns an error (which previously caused the dir to leak).
	// On success the workspace-conditional closure (registered below) also runs
	// first (LIFO) and either moves or removes the directory; the safety closure
	// then runs os.RemoveAll on the already-gone path which is a no-op.
	destDir := tempCloneDir()
	reg.Register(func() error {
		return os.RemoveAll(destDir)
	})
	cloneRoot, err := deps.Cloner.Clone(ctx, domain.CloneOptions{
		RepoURL: cfg.RepoURL,
		Branch:  cfg.BaseBranch,
		Token:   cfg.Token,
		DestDir: destDir,
	})
	if err != nil {
		return fail("clone", "git clone failed", err)
	}
	log.Debug("clone destination", "clone_root", cloneRoot)
	// Record the clone root in cs so runCleanupStep can describe the workspace
	// disposition in the cleanup trace.
	cs.cloneRoot = cloneRoot
	// Register status-conditional cleanup for the ephemeral clone directory.
	//
	// The closure captures cs (by value copy of the pointer fields) and reads
	// *cs.finalStatus at run time, which reflects the terminal overallStatus.
	// This is the core of the status-conditional workspace retention:
	//
	//   - PASSED and !KeepWorkspace → os.RemoveAll(cloneRoot) — normal success cleanup
	//   - Any other outcome (FAILED, ABORTED, USAGE_ERROR) OR KeepWorkspace=true →
	//     MoveWorkspace(cloneRoot, <runDir>/workspace/) — retain the clone for debugging
	//
	// When cs.runDir is empty (run-dir creation failed / degraded mode), fall back
	// to unconditional removal to avoid leaking temp dirs.
	capturedCS := cs // snapshot after cloneRoot is set
	reg.Register(func() error {
		// Remove on success (unless keep-workspace requested) or when runDir is empty.
		if capturedCS.finalStatus != nil && *capturedCS.finalStatus == domain.StatusPassed && !capturedCS.keepWorkspace {
			return os.RemoveAll(capturedCS.cloneRoot)
		}
		if capturedCS.runDir == "" {
			// Degraded mode: run-dir not available, fall back to removal.
			slog.Warn("run dir unavailable; removing clone dir (cannot retain workspace)", "cloneRoot", capturedCS.cloneRoot)
			return os.RemoveAll(capturedCS.cloneRoot)
		}
		// Move the clone into <runDir>/workspace/ for post-mortem inspection.
		return MoveWorkspace(capturedCS.cloneRoot, filepath.Join(capturedCS.runDir, "workspace"))
	})
	{
		cloneTrace := fmt.Sprintf(
			"repo    %s\nbranch  %s\ndest    %s\nstructure: liquibase.properties, master-changelog/ expected",
			redactURL(cfg.RepoURL),
			cfg.BaseBranch,
			cloneRoot,
		)
		passWithTrace("clone", time.Since(t0), cloneTrace)
	}

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
	log.Info("step.start", "step", "engine-guard")
	notify(deps, "engine-guard", false, false)
	t0 = time.Now()
	propsPath := cloneRoot + "/src/main/resources/db/liquibase.properties"
	detectedEngine, err := deps.Engine.Detect(propsPath)
	if err != nil {
		return fail("engine-guard", "engine detection failed", err)
	}
	log.Debug("engine resolved", "engine", detectedEngine)
	passWithTrace("engine-guard", time.Since(t0),
		fmt.Sprintf("detected engine: %s", detectedEngine))

	// --- Step 3b: SQLInput overlay ---
	// Copy the developer's local SQLInput tree into the clone's
	// src/main/resources/SQLInput/ directory before starting any container or Maven.
	// When deps.Overlayer is nil (legacy / test without overlay), skip this step.
	if deps.Overlayer != nil {
		log.Info("step.start", "step", "overlay", "sql_input_src", cfg.SQLInputPath)
		notify(deps, "overlay", false, false)
		// Collect the list of SQL files before copying — logged so engineers can see
		// exactly which files were overlaid into the clone.
		overlayFiles := collectSQLFileNames(cfg.SQLInputPath)
		log.Debug("overlay file list", "files", strings.Join(overlayFiles, ", "), "count", len(overlayFiles))
		t0 = time.Now()
		destSQLInput := cloneRoot + "/src/main/resources/SQLInput"
		copiedCount, err := deps.Overlayer.Apply(cfg.SQLInputPath, destSQLInput)
		if err != nil {
			return fail("overlay", "SQLInput overlay failed", err)
		}
		{
			var overlayLines []string
			overlayLines = append(overlayLines,
				fmt.Sprintf("files copied: %d  (src: %s → dest: %s)",
					copiedCount, cfg.SQLInputPath, destSQLInput))
			for _, f := range overlayFiles {
				overlayLines = append(overlayLines, "  "+f)
			}
			passWithTrace("overlay", time.Since(t0), strings.Join(overlayLines, "\n"))
		}
	}

	// --- Step 4: Start container ---
	log.Info("step.start", "step", "container-start",
		"image", deps.DBProvider.Image(),
		"network", deps.NetworkName,
	)
	notify(deps, "container-start", false, false)
	t0 = time.Now()
	containerProvider := deps.DBProvider.ContainerProvider()
	coords, err := containerProvider.Start(ctx)
	if err != nil {
		return fail("container-start", "failed to start ephemeral Postgres container", err)
	}
	// Register cleanup for the container and mark it as created in cs so that
	// buildCleanupTrace can report "eliminado" vs "n/a (no se creó)" accurately.
	reg.Register(func() error {
		return containerProvider.Stop(context.Background())
	})
	cs.containerRegistered = true
	// Log the connection alias (no credentials) so engineers can see which network
	// alias the Maven container will use to reach Postgres.
	pgAlias := coords.AliasHost
	if pgAlias == "" {
		pgAlias = coords.Host
	}
	log.Debug("postgres container up",
		"host", coords.Host,
		"port", coords.Port,
		"alias_host", pgAlias,
		"alias_port", coords.AliasPort,
		"db", coords.DBName,
		"user", coords.User,
	)
	{
		containerTrace := fmt.Sprintf(
			"image   %s\nhost    %s  port %d  (host-mapped)\nalias   %s:%d  (Docker network alias for Maven container)\ndb      %s  user %s",
			deps.DBProvider.Image(),
			coords.Host, coords.Port,
			pgAlias, coords.AliasPort,
			coords.DBName, coords.User,
		)
		if deps.NetworkName != "" {
			containerTrace += fmt.Sprintf("\nnetwork %s", deps.NetworkName)
		}
		passWithTrace("container-start", time.Since(t0), containerTrace)
	}

	// --- Step 5: Readiness probe ---
	log.Info("step.start", "step", "readiness-probe")
	notify(deps, "readiness-probe", false, false)
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
	{
		readinessElapsed := time.Since(t0)
		readinessTrace := fmt.Sprintf(
			"database accepting connections\nprobe elapsed: %s  (deadline: %s, initial interval: %s)",
			readinessElapsed.Round(time.Millisecond),
			policy.Deadline,
			policy.InitialInterval,
		)
		passWithTrace("readiness-probe", readinessElapsed, readinessTrace)
	}

	// --- Step 6a: Schema extraction + lb_<schema> user + GRANT-target roles ---
	// Extract the target schema name from the archetype DDL to derive the
	// ephemeral connection user (lb_<schema>) and auto-create GRANT-target roles.
	notify(deps, "schema-setup", false, false)
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
	{
		schemaSetupDur := time.Since(t0)
		var schemaLines []string
		if schemaName != "" {
			grantRoles, _ := liquibase.ExtractGrantTargetRolesFromArchetype(cloneRoot)
			schemaLines = append(schemaLines,
				fmt.Sprintf("schema          %s", schemaName),
				fmt.Sprintf("lb user         %s  (password: [REDACTED])", lbUsername),
				fmt.Sprintf("bookkeeping schema: %s", lbUsername),
			)
			if len(grantRoles) > 0 {
				schemaLines = append(schemaLines,
					fmt.Sprintf("grant-target roles created: %s", strings.Join(grantRoles, ", ")))
			}
		} else {
			schemaLines = append(schemaLines,
				fmt.Sprintf("no schema found in archetype DDL; using admin user: %s", lbUsername),
			)
		}
		passWithTrace("schema-setup", schemaSetupDur, strings.Join(schemaLines, "\n"))
	}

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
	log.Debug("step.start", "step", "pom-driver-inject")
	notify(deps, "pom-driver-inject", false, false)
	t0 = time.Now()
	clonedPomPath := cloneRoot + "/pom.xml"
	if err := maven.InjectDriverDependency(clonedPomPath); err != nil {
		return fail("pom-driver-inject", "failed to inject PostgreSQL driver into cloned pom.xml", err)
	}
	passWithTrace("pom-driver-inject", time.Since(t0),
		fmt.Sprintf("injected driver: %s:%s:%s into %s",
			"org.postgresql", "postgresql", maven.PostgresDriverVersion, clonedPomPath))

	// --- Step 6c: Patch liquibase.properties ---
	log.Info("step.start", "step", "properties-patch",
		"props_path", propsPath,
		"alias_host", lbCoords.AliasHost,
		"alias_port", lbCoords.AliasPort,
		"db_user", lbCoords.User,
		"db_name", lbCoords.DBName,
	)
	notify(deps, "properties-patch", false, false)
	t0 = time.Now()
	if err := deps.Patcher.Patch(propsPath, lbCoords); err != nil {
		return fail("properties-patch", "failed to patch liquibase.properties", err)
	}
	{
		jdbcHost := lbCoords.Host
		jdbcPort := lbCoords.Port
		if lbCoords.AliasHost != "" {
			jdbcHost = lbCoords.AliasHost
			jdbcPort = lbCoords.AliasPort
		}
		jdbcURL := fmt.Sprintf("jdbc:postgresql://%s:%d/%s", jdbcHost, jdbcPort, lbCoords.DBName)
		passWithTrace("properties-patch", time.Since(t0),
			fmt.Sprintf("url      %s\nusername %s\npassword [REDACTED]",
				jdbcURL, lbCoords.User))
	}

	// --- Step 6d: Pre-sync validation (optional seam) ---
	// When a PreSyncValidator is wired, it runs here — before the ephemeral sync —
	// mirroring the real pipeline's validate → validate-ephemeral order.
	// A nil validator is treated as the no-op default (always passes).
	log.Debug("step.start", "step", "pre-sync-validate")
	notify(deps, "pre-sync-validate", false, false)
	t0 = time.Now()
	preSyncValidator := deps.PreSyncValidator
	if preSyncValidator == nil {
		preSyncValidator = domain.NoOpPreSyncValidator{}
	}
	isNoOp := deps.PreSyncValidator == nil
	if err := preSyncValidator.ValidatePreSync(ctx, cloneRoot); err != nil {
		return fail("pre-sync-validate", "pre-sync validation failed", err)
	}
	{
		var preSyncNote string
		if isNoOp {
			preSyncNote = "SQL rules validator not enabled (no-op seam); step is a pass-through"
		} else {
			preSyncNote = fmt.Sprintf("SQL rules validator ran against %s — passed", cloneRoot)
		}
		passWithTrace("pre-sync-validate", time.Since(t0), preSyncNote)
	}

	// Resolve the Maven output writer: use MavenOut if provided, otherwise discard.
	// MavenOut is wired in main.go to the log file only; Maven verbose output
	// goes to execution.log rather than the console, which stays quiet.
	mavenOut := deps.MavenOut
	if mavenOut == nil {
		mavenOut = io.Discard
	}

	// --- Step 7: dbflow:sync ---
	syncParamList := syncParams()
	log.Info("step.start", "step", maven.GoalSync,
		"goal", maven.GoalSync,
		"mvn_cmd", buildRedactedMvnCmd(maven.GoalSync, syncParamList),
		"network", deps.NetworkName,
	)
	notify(deps, maven.GoalSync, false, false)
	t0 = time.Now()
	syncResult, err := deps.Maven.Run(ctx, cloneRoot, maven.GoalSync, syncParamList, mavenOut)
	if err != nil {
		return fail(maven.GoalSync, "maven runner error during sync", err)
	}
	syncResult.Duration = time.Since(t0)
	syncResult.DurationMs = syncResult.Duration.Milliseconds()
	log.Debug("step.done", "step", maven.GoalSync, "duration_ms", syncResult.DurationMs, "status", syncResult.Status)
	notify(deps, maven.GoalSync, true, syncResult.Status != domain.StepStatusPassed)
	steps = append(steps, syncResult)
	if syncResult.Status != domain.StepStatusPassed {
		overallStatus = domain.StatusFailed
		return appendCleanupAndBuild()
	}

	// --- Step 8: First-tag resolution ---
	log.Info("step.start", "step", "first-tag")
	notify(deps, "first-tag", false, false)
	t0 = time.Now()
	firstTag, err := deps.Tags.FirstTag(cloneRoot)
	if err != nil {
		return fail("first-tag", "first-tag resolution failed", err)
	}
	log.Debug("first-tag resolved", "tag", firstTag)
	passWithTrace("first-tag", time.Since(t0),
		fmt.Sprintf("resolved rollback tag: %s  (from master-changelog)", firstTag))

	// --- Step 9: dbflow:rollback ---
	rollbackParamList := rollbackParams(firstTag)
	log.Info("step.start", "step", maven.GoalRollback,
		"goal", maven.GoalRollback,
		"tag", firstTag,
		"mvn_cmd", buildRedactedMvnCmd(maven.GoalRollback, rollbackParamList),
		"network", deps.NetworkName,
	)
	notify(deps, maven.GoalRollback, false, false)
	t0 = time.Now()
	rollbackResult, err := deps.Maven.Run(ctx, cloneRoot, maven.GoalRollback, rollbackParamList, mavenOut)
	if err != nil {
		return fail(maven.GoalRollback, "maven runner error during rollback", err)
	}
	rollbackResult.Duration = time.Since(t0)
	rollbackResult.DurationMs = rollbackResult.Duration.Milliseconds()
	log.Debug("step.done", "step", maven.GoalRollback, "duration_ms", rollbackResult.DurationMs, "status", rollbackResult.Status)
	notify(deps, maven.GoalRollback, true, rollbackResult.Status != domain.StepStatusPassed)
	steps = append(steps, rollbackResult)
	if rollbackResult.Status != domain.StepStatusPassed {
		overallStatus = domain.StatusFailed
	}

	return appendCleanupAndBuild()
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

// collectSQLFileNames returns the base names of all .sql files found directly
// under dir (recursive). Used to log the overlay file list before Apply is called.
// On any error, returns the files collected so far (best-effort).
func collectSQLFileNames(dir string) []string {
	var names []string
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(d.Name()), ".sql") {
			names = append(names, d.Name())
		}
		return nil
	})
	return names
}

// buildRedactedMvnCmd returns a human-readable representation of the Maven
// command that will be executed inside the container. Parameters are included
// as-is; the goal is shown. This is safe to log — callers must never include
// raw tokens in params.
func buildRedactedMvnCmd(goal string, params []string) string {
	parts := []string{
		"mvn", "-f", "/work/pom.xml", "-B",
		"-s", "/work/settings.xml",
		"-Dmaven.repo.local=/m2",
		goal,
	}
	parts = append(parts, "-Dparams="+strings.Join(params, " "))
	return strings.Join(parts, " ")
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
