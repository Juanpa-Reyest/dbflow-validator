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
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/dbflow-validator/dbflow-validator/internal/config"
	"github.com/dbflow-validator/dbflow-validator/internal/container"
	"github.com/dbflow-validator/dbflow-validator/internal/domain"
	"github.com/dbflow-validator/dbflow-validator/internal/engine"
	"github.com/dbflow-validator/dbflow-validator/internal/git"
	"github.com/dbflow-validator/dbflow-validator/internal/liquibase"
	"github.com/dbflow-validator/dbflow-validator/internal/maven"
	"github.com/dbflow-validator/dbflow-validator/internal/orchestrator"
	"github.com/dbflow-validator/dbflow-validator/internal/preflight"
	"github.com/dbflow-validator/dbflow-validator/internal/report"
)

func main() {
	os.Exit(run(os.Args[1:], os.Getenv))
}

// run is the testable entry point. It returns the process exit code.
func run(args []string, env func(string) string) int {
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
	pgProvider := container.NewPostgresProvider()
	dbEng, err := engine.ProviderFor(engine.EnginePostgres)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dbflow-validator: engine provider: %v\n", err)
		return 2
	}

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
		Maven:   maven.NewRunner(""),
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
func exitCode(s domain.Status) int {
	switch s {
	case domain.StatusPassed:
		return 0
	case domain.StatusFailed:
		return 1
	case domain.StatusAborted:
		return 130
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
