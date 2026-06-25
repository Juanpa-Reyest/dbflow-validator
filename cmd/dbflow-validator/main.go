// Command dbflow-validator validates a PostgreSQL Maven DB archetype by running
// sync + rollback against an ephemeral local Postgres container.
//
// Usage:
//
//	dbflow-validator --repo-url <url> [--base-branch <branch>] [--output-format console|json] [--output-file <path>]
//
// The git token MUST be set via DBFLOW_GIT_TOKEN environment variable.
// Exit codes: 0=PASSED, 1=FAILED, 2=config/usage error, 130=ABORTED (signal).
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/dbflow-validator/dbflow-validator/internal/config"
	"github.com/dbflow-validator/dbflow-validator/internal/container"
	"github.com/dbflow-validator/dbflow-validator/internal/domain"
	"github.com/dbflow-validator/dbflow-validator/internal/embedrepo"
	"github.com/dbflow-validator/dbflow-validator/internal/embedvalidator"
	"github.com/dbflow-validator/dbflow-validator/internal/engine"
	"github.com/dbflow-validator/dbflow-validator/internal/git"
	"github.com/dbflow-validator/dbflow-validator/internal/liquibase"
	"github.com/dbflow-validator/dbflow-validator/internal/logging"
	"github.com/dbflow-validator/dbflow-validator/internal/maven"
	"github.com/dbflow-validator/dbflow-validator/internal/orchestrator"
	"github.com/dbflow-validator/dbflow-validator/internal/overlay"
	"github.com/dbflow-validator/dbflow-validator/internal/preflight"
	"github.com/dbflow-validator/dbflow-validator/internal/report"
	"github.com/dbflow-validator/dbflow-validator/internal/rundir"
	"github.com/dbflow-validator/dbflow-validator/internal/rulesvalidator"
)

// buildVersion is injected at link time via -ldflags "-X main.buildVersion=<version>".
// It is used as the per-version cache directory key for the extracted Maven repo.
// Falls back to "dev" when not set (go run / go test without ldflags).
var buildVersion = "dev"

// usageText is the help text printed when --help / -h is requested.
const usageText = `dbflow-validator — validate a PostgreSQL Maven DB archetype

Usage:
  dbflow-validator --repo-url <url> [flags]
  dbflow-validator              (interactive TTY: prompts for URL and token)

Flags:
  --repo-url      string   Git repository URL to clone and validate (required, or interactive)
  --base-branch   string   Branch to validate (default: integration)
  --sql-input     string   Path to local SQLInput directory (default: ./src/main/resources/SQLInput)
  --output-format string   Output format: console or json (default: console)
  --output-file   string   Path to write JSON output (optional)
  --log-level     string   Log verbosity: debug, info, warn, error (default: info)
  --output-dir    string   Directory for per-run artifact subdirectories (default: ./dbflow-validator-runs)
  --keep-workspace         Retain the ephemeral clone under <run>/workspace/ even on a PASSED run
  --version / -v           Print version and exit
  --help / -h              Print this help and exit

Environment variables:
  DBFLOW_GIT_TOKEN   Git access token (alternative to interactive prompt; never logged)

Examples:
  # Non-interactive (flags + env var):
  DBFLOW_GIT_TOKEN=<token> dbflow-validator \
    --repo-url https://github.com/org/db-artifacts-myproject.git \
    --base-branch integration \
    --output-format console

  # JSON output to file:
  DBFLOW_GIT_TOKEN=<token> dbflow-validator \
    --repo-url https://github.com/org/db-artifacts-myproject.git \
    --output-format json \
    --output-file result.json

  # Retain workspace on any outcome (useful for debugging):
  DBFLOW_GIT_TOKEN=<token> dbflow-validator \
    --repo-url https://github.com/org/db-artifacts-myproject.git \
    --keep-workspace

  # Interactive (TTY): prompts for URL and token when not provided:
  dbflow-validator

Run artifacts:
  Each run creates a timestamped subdirectory under --output-dir:
    <output-dir>/<20060102T150405Z>/
      execution.log   Full verbose trace (always written, regardless of --log-level)
      report.json     Machine-readable validation result (always written)
      workspace/      Clone retained here on FAILED runs (or when --keep-workspace is set)

  To prune old runs: rm -rf dbflow-validator-runs/
  The dbflow-validator-runs/ directory and the binary itself are listed in .gitignore.

Exit codes:
  0   Validation PASSED
  1   Validation FAILED
  2   Configuration or usage error
  130 Aborted by SIGINT/SIGTERM
`

func main() {
	os.Exit(run(os.Args[1:], os.Getenv))
}

// run is the testable entry point. It returns the process exit code.
// Version and help output go to os.Stdout.
func run(args []string, env func(string) string) int {
	// Disable the Ryuk reaper before any testcontainers call. This is the FIRST
	// side-effectful statement in the entry point so it fires unconditionally on every OS.
	// Cleanup is already handled by CleanupRegistry (LIFO, run-once, eager+deferred RunAll),
	// so Ryuk is redundant and its absence does not weaken any cleanup guarantee.
	os.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true") //nolint:errcheck // always succeeds
	return runWithHelpOutput(args, env, os.Stdout, os.Stdout)
}

// runWithOutput is kept for backward compatibility with existing tests.
// helpOut and versionOut are both directed to the same writer.
func runWithOutput(args []string, env func(string) string, versionOut io.Writer) int {
	return runWithHelpOutput(args, env, versionOut, versionOut)
}

// runWithHelpOutput is the fully injectable entry point used by tests.
// helpOut receives the help text when --help / -h is requested.
// versionOut receives the version line when --version / -v is requested.
func runWithHelpOutput(args []string, env func(string) string, helpOut io.Writer, versionOut io.Writer) int {
	// Handle --help / -h and --version / -v before flag parsing so they always
	// work regardless of other flag state and never trigger "flag provided but
	// not defined" errors.
	for _, a := range args {
		switch a {
		case "--help", "-h":
			fmt.Fprint(helpOut, usageText)
			return 0
		case "--version", "-v":
			fmt.Fprintf(versionOut, "dbflow-validator %s\n", buildVersion)
			return 0
		}
	}

	// --- 1. Config resolution ---
	cfg, err := config.Resolve(args, env)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dbflow-validator: %v\n", err)
		return 2
	}

	// Resolve log level before creating the dual-sink logger.
	logLevel := slog.LevelInfo
	if cfg.LogLevel == "debug" {
		logLevel = slog.LevelDebug
	}

	// --- 2. Create run dir early (before any network or orchestrator ops) ---
	// Creating early ensures even early failures get an execution.log.
	// On failure: warn to console, set runDir = "" (degraded mode — console only).
	runDirPath := rundir.RunDirPath(cfg.OutputDir, time.Now())
	var logFile *os.File
	if err := os.MkdirAll(runDirPath, 0o700); err != nil {
		slog.Warn("could not create run dir; run artifacts will not be written",
			"dir", runDirPath, "err", err)
		runDirPath = "" // degraded mode
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))
	} else {
		// Open execution.log inside the run dir.
		logFilePath := filepath.Join(runDirPath, "execution.log")
		lf, openErr := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if openErr != nil {
			slog.Warn("could not open execution.log; file logging disabled",
				"path", logFilePath, "err", openErr)
			slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))
		} else {
			logFile = lf
			// Install file-only logger: all slog output (verbose step trace) goes
			// exclusively to execution.log. The console stays quiet — clean progress
			// lines are emitted via deps.OnStep (fmt-based, not slog).
			fileLogger := logging.NewFileSink(logFile)
			slog.SetDefault(fileLogger)
		}
	}

	// logFile is closed manually after the structured execution.log is written.
	// Do NOT defer here — we need to reopen/truncate it after the run.

	// --- 3. Signal-safe context ---
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Second signal hard-exits.
	go func() {
		<-ctx.Done()
		stop() // release the signal catcher
		// If a second signal arrives while still running, exit immediately.
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
		<-ch
		slog.Warn("second signal received; forcing exit")
		os.Exit(130)
	}()

	// --- 4. Print banner at the very top of console output ---
	// The banner is emitted once here, before the orchestrator starts and before
	// any live progress line (▸ step …). RenderQuiet (step 8) must NOT re-emit it.
	fmt.Fprint(os.Stdout, report.Banner(buildVersion))

	// --- 5. Wire concrete adapters ---
	// The Docker network is created LAZILY inside the orchestrator at container-start.
	// Early failures (preflight, clone, engine-guard, overlay) never create a network.
	// The NetworkFactory closure captures ctx so the network removal uses the same
	// context as the run (cancelled on SIGINT/SIGTERM).
	pgProvider := container.NewPostgresProvider()
	dbEng, err := engine.ProviderFor(engine.EnginePostgres)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dbflow-validator: engine provider: %v\n", err)
		return 2
	}

	// Extract embedded vendored Maven repo to the per-version cache dir.
	// For the container runner, this path is mounted at /m2 inside the Maven container.
	mavenRepoCachePath := resolveEmbeddedMavenCache()

	// Build the Maven output writer: routes Maven stdout/stderr exclusively to
	// execution.log. The console stays quiet during a run; the OnStep callback
	// prints the clean progress line when each Maven goal completes.
	// When logFile is nil (degraded mode), Maven output is discarded.
	var mavenOut io.Writer = io.Discard
	if logFile != nil {
		mavenOut = logFile
	}

	uid, gid := hostUIDGID()

	// Extract embedded validator JAR to the per-version user cache directory.
	// Fail-CLOSED: if extraction fails the run aborts with a clear error rather
	// than silently disabling the gate (which would violate the fail-safe principle).
	validatorJARPath, jarErr := resolveEmbeddedValidatorJar()
	if jarErr != nil {
		fmt.Fprintf(os.Stderr, "dbflow-validator: cannot extract embedded validator JAR — aborting to preserve fail-safe gate: %v\n", jarErr)
		fmt.Fprintf(os.Stderr, "dbflow-validator: tip: ensure the binary was built with //go:embed assets and the cache directory is writable (%s)\n", defaultCacheRoot())
		return 2
	}

	// Build the Maven container runner with an empty network name — the orchestrator
	// will call SetNetworkName on it after the network is created at container-start.
	mavenRunner := maven.NewContainerRunner(maven.DefaultImage, "", mavenRepoCachePath, uid, gid)
	deps := orchestrator.Deps{
		Preflight: preflight.New(nil),
		Cloner:    git.NewCloner(nil, nil),
		DBProvider: &postgresDBProvider{
			eng:      dbEng,
			provider: pgProvider,
		},
		Patcher: liquibase.NewPatcher(),
		Engine:  engine.NewDetector(),
		Tags:    &liquibase.ChangelogResolver{},
		Maven:   mavenRunner,
		// NetworkFactory creates the Docker network lazily when the flow reaches
		// container-start. If the run fails before that step, no network is created.
		// container.NewNetwork returns (id, name, cleanup, err); the factory
		// signature is (ctx) → (name, cleanup, err) so we wrap with a closure.
		NetworkFactory: func(ctx context.Context) (string, func() error, error) {
			_, name, cleanup, err := container.NewNetwork(ctx)
			return name, cleanup, err
		},
		MavenRepoCachePath: mavenRepoCachePath,
		Overlayer:          overlay.New(),
		MavenOut:           mavenOut,
		RunDir:             runDirPath,
		KeepWorkspace:      cfg.KeepWorkspace,
		// OnStep emits a clean one-line-per-step progress update to stdout.
		// "starting" events print "  ▸ <name> …" and "done" events print the result.
		// This keeps the console quiet (no slog logfmt) while still showing activity.
		OnStep: consoleProgressPrinter(os.Stdout),
	}

	// Wire the concrete PreSyncValidator. validatorJARPath is guaranteed non-empty
	// here because extraction failure aborts the run above (fail-CLOSED gate).
	deps.PreSyncValidator = rulesvalidator.New(
		maven.DefaultImage, validatorJARPath, uid, gid, nil,
		// Route the validator container's stdout/stderr to execution.log (same
		// sink as Maven), so the JAR's output is always kept as evidence — on a
		// passing run and, crucially, on a failing one for diagnosis.
		rulesvalidator.WithValidatorOut(mavenOut),
	)

	// --- 6. Run orchestration ---
	rpt := orchestrator.Run(ctx, deps, cfg)

	// --- 7. Close live log file and render structured execution.log ---
	// During the run the dual-sink logger wrote a raw live trace to execution.log
	// (crash-safety). Now that the run is complete, we close the file, render the
	// final structured document (banner + summary table + block traces), and
	// overwrite execution.log with the human-readable result.
	if logFile != nil {
		_ = logFile.Close()
		logFile = nil
	}
	runID := filepath.Base(runDirPath)
	if runDirPath != "" {
		execLogDoc := report.RenderExecLog(rpt, runID, buildVersion, "")
		logFilePath := filepath.Join(runDirPath, "execution.log")
		if err := os.WriteFile(logFilePath, []byte(execLogDoc), 0o600); err != nil {
			slog.Warn("could not write structured execution.log", "path", logFilePath, "err", err)
		}
	}

	// --- 8. Console summary (step table + RESULT + execution.log pointer) ---
	// Banner was already printed at step 4 (before orchestrator ran). Do NOT re-emit it here.
	report.NewConsoleRenderer().RenderQuiet(rpt, runDirPath, os.Stdout)

	// --- 9. JSON output (when requested via --output-format or --output-file) ---
	if cfg.OutputFormat == "json" || cfg.OutputFile != "" {
		jsonRenderer := report.NewJSONRenderer()
		jsonBytes, err := jsonRenderer.Render(rpt)
		if err != nil {
			slog.Error("JSON render failed", "err", err)
		} else {
			if cfg.OutputFile != "" {
				if err := os.WriteFile(cfg.OutputFile, jsonBytes, 0o644); err != nil {
					slog.Error("write output file", "path", cfg.OutputFile, "err", err)
				}
			} else {
				// Print JSON to stdout if format=json and no output-file.
				os.Stdout.Write(jsonBytes)
				fmt.Println()
			}
		}
	}

	// --- 10. Always write report.json to the run dir ---
	// This is written regardless of --output-format so every run leaves a
	// machine-readable record for post-mortem inspection.
	if runDirPath != "" {
		jsonRenderer := report.NewJSONRenderer()
		jsonBytes, renderErr := jsonRenderer.Render(rpt)
		if renderErr != nil {
			slog.Warn("could not render report.json for run dir", "err", renderErr)
		} else {
			reportPath := filepath.Join(runDirPath, "report.json")
			if err := os.WriteFile(reportPath, jsonBytes, 0o644); err != nil {
				slog.Warn("could not write report.json to run dir", "path", reportPath, "err", err)
			}
		}
	}

	// --- 11. Exit code ---
	return exitCode(rpt.Status)
}

// exitCode maps domain.Status to a UNIX exit code.
//
// Exit code contract:
//
//	0  — PASSED
//	1  — FAILED (validation failure — sync or rollback failed)
//	2  — USAGE_ERROR or unknown (config/usage error: missing SQLInput, bad flags, etc.)
//	130 — ABORTED (SIGINT / SIGTERM)
func exitCode(s domain.Status) int {
	switch s {
	case domain.StatusPassed:
		return 0
	case domain.StatusFailed:
		return 1
	case domain.StatusAborted:
		return 130
	case domain.StatusUsageError:
		return 2
	default:
		return 2
	}
}

// postgresDBProvider adapts container.PostgresProvider and engine.DatabaseProvider
// into the domain.DatabaseProvider interface expected by the orchestrator.
type postgresDBProvider struct {
	eng      domain.DatabaseProvider
	provider *container.PostgresProvider
}

func (p *postgresDBProvider) Image() string { return p.eng.Image() }
func (p *postgresDBProvider) ContainerProvider() domain.ContainerProvider {
	return p.provider
}
func (p *postgresDBProvider) DSN(coords domain.ContainerCoords) string {
	return p.eng.DSN(coords)
}
func (p *postgresDBProvider) Ping(ctx context.Context, dsn string) error {
	return container.Ping(ctx, dsn)
}

// resolveEmbeddedMavenCache extracts the embedded vendored Maven repository to
// the per-version user cache directory and returns the extraction path.
//
// Cache location: ~/.cache/dbflow-validator/<buildVersion>/m2
//
// On failure, a warning is logged and "" is returned. The Maven container will
// run without the /m2 mount and will produce a clear error about missing artifacts
// rather than a silent failure.
func resolveEmbeddedMavenCache() string {
	cacheRoot := defaultCacheRoot()
	repoPath, err := embedrepo.EnsureExtracted(cacheRoot, buildVersion)
	if err != nil {
		slog.Warn("failed to extract embedded Maven repo; Maven container may fail offline resolution",
			"err", err, "cacheRoot", cacheRoot, "version", buildVersion)
		return ""
	}
	return repoPath
}

// defaultCacheRoot returns the OS-appropriate user cache root for dbflow-validator.
// On Linux/macOS: ~/.cache/dbflow-validator
// On Windows:     %LOCALAPPDATA%\dbflow-validator
func defaultCacheRoot() string {
	if runtime.GOOS == "windows" {
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			return filepath.Join(localAppData, "dbflow-validator")
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		// Fallback: use temp dir (unusual but safe).
		return filepath.Join(os.TempDir(), "dbflow-validator")
	}
	return filepath.Join(home, ".cache", "dbflow-validator")
}

// hostUIDGID returns the host process's UID and GID for --user in the Maven container.
// This ensures files written into /work (the mounted clone dir) are owned by the
// host user, not root, so os.RemoveAll in cleanup succeeds without permission errors.
func hostUIDGID() (int, int) {
	return os.Getuid(), os.Getgid()
}

// consoleProgressPrinter returns an orchestrator.StepFunc that writes a clean
// one-line-per-step progress indicator to w (typically os.Stdout).
//
// Format for starting events (Done=false):
//
//	  ▸ preflight …
//
// Format for completed events (Done=true):
//
//	  ▸ preflight … OK
//	  ▸ clone … FAILED
//
// These are plain fmt lines, not slog logfmt, so the console stays free of
// "time=… level=… msg=…" noise during a run.
func consoleProgressPrinter(w io.Writer) func(orchestrator.StepEvent) {
	return func(e orchestrator.StepEvent) {
		if !e.Done {
			fmt.Fprintf(w, "  ▸ %s …\n", e.Name)
			return
		}
		result := "OK"
		if e.Failed {
			result = "FAILED"
		}
		fmt.Fprintf(w, "  ▸ %s … %s\n", e.Name, result)
	}
}

// resolveValidatorJarWith extracts the embedded SQL rules validator JAR using the
// provided extractor function and returns the path or an error (fail-CLOSED).
//
// This is the testable core: production callers pass embedvalidator.EnsureExtracted;
// tests can inject a stub that simulates extraction failure.
func resolveValidatorJarWith(extractor func(cacheRoot, version string) (string, error), cacheRoot, version string) (string, error) {
	return extractor(cacheRoot, version)
}

// resolveEmbeddedValidatorJar extracts the embedded SQL rules validator JAR to
// the per-version user cache directory and returns the extraction path.
//
// Cache location: ~/.cache/dbflow-validator/<buildVersion>/validator/validator.jar
//
// On failure an error is returned — the caller MUST abort rather than silently
// fall back to the no-op gate, preserving the fail-safe principle.
func resolveEmbeddedValidatorJar() (string, error) {
	cacheRoot := defaultCacheRoot()
	return resolveValidatorJarWith(embedvalidator.EnsureExtracted, cacheRoot, buildVersion)
}
