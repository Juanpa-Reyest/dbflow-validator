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

	"github.com/dbflow-validator/dbflow-validator/internal/config"
	"github.com/dbflow-validator/dbflow-validator/internal/container"
	"github.com/dbflow-validator/dbflow-validator/internal/domain"
	"github.com/dbflow-validator/dbflow-validator/internal/embedrepo"
	"github.com/dbflow-validator/dbflow-validator/internal/engine"
	"github.com/dbflow-validator/dbflow-validator/internal/git"
	"github.com/dbflow-validator/dbflow-validator/internal/liquibase"
	"github.com/dbflow-validator/dbflow-validator/internal/maven"
	"github.com/dbflow-validator/dbflow-validator/internal/orchestrator"
	"github.com/dbflow-validator/dbflow-validator/internal/preflight"
	"github.com/dbflow-validator/dbflow-validator/internal/report"
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

  # Interactive (TTY): prompts for URL and token when not provided:
  dbflow-validator

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

	// Configure structured logging.
	logLevel := slog.LevelInfo
	if cfg.LogLevel == "debug" {
		logLevel = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))

	// --- 2. Signal-safe context ---
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

	// --- 3. Wire concrete adapters ---

	// Create per-run Docker network so Postgres and Maven containers share a DNS alias.
	// The network cleanup is registered in orchestrator.Run via deps.NetworkCleanup (LIFO).
	_, networkName, networkCleanup, netErr := container.NewNetwork(ctx)
	if netErr != nil {
		fmt.Fprintf(os.Stderr, "dbflow-validator: create docker network: %v\n", netErr)
		return 2
	}
	// Ensure network is removed even if orchestrator.Run panics or exits early.
	defer func() { _ = networkCleanup() }()

	pgProvider := container.NewPostgresProvider(networkName)
	dbEng, err := engine.ProviderFor(engine.EnginePostgres)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dbflow-validator: engine provider: %v\n", err)
		return 2
	}

	// Extract embedded vendored Maven repo to the per-version cache dir.
	// For the container runner, this path is mounted at /m2 inside the Maven container.
	mavenRepoCachePath := resolveEmbeddedMavenCache()

	uid, gid := hostUIDGID()
	deps := orchestrator.Deps{
		Preflight: preflight.New(nil),
		Cloner:    git.NewCloner(nil, nil),
		DBProvider: &postgresDBProvider{
			eng:      dbEng,
			provider: pgProvider,
		},
		Patcher:            liquibase.NewPatcher(),
		Engine:             engine.NewDetector(),
		Tags:               &liquibase.ChangelogResolver{},
		Maven:              maven.NewContainerRunner(maven.DefaultImage, networkName, mavenRepoCachePath, uid, gid),
		NetworkCleanup:     networkCleanup,
		MavenRepoCachePath: mavenRepoCachePath,
	}

	// --- 4. Run orchestration ---
	rpt := orchestrator.Run(ctx, deps, cfg)

	// --- 5. Console output (always) ---
	consoleRenderer := report.NewConsoleRenderer()
	consoleRenderer.Render(rpt, os.Stdout)

	// --- 6. JSON output (when requested) ---
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

	// --- 7. Exit code ---
	return exitCode(rpt.Status)
}

// exitCode maps domain.Status to a UNIX exit code.
//
// Exit code contract:
//   0  — PASSED
//   1  — FAILED (validation failure — sync or rollback failed)
//   2  — USAGE_ERROR or unknown (config/usage error: missing SQLInput, bad flags, etc.)
//   130 — ABORTED (SIGINT / SIGTERM)
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
