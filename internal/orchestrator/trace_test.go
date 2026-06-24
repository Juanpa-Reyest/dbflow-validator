package orchestrator_test

// trace_test.go — TDD tests for the verbose step-by-step trace feature.
//
// These tests verify that orchestrator.Run emits structured log lines through
// the injected logger for each enumerated detail:
//   - Step start/end lines with step name and timing
//   - Redacted mvn command line (token/secret absent)
//   - Docker network name
//   - Overlay file list (at least one sql file)
//   - Resolved first-tag (used for rollback)
//   - Clone destination/workspace path
//   - Resolved engine name
//   - Connection alias (no credentials)
//
// All tests use fake port implementations and a capturing slog.Handler so they
// run under -short without Docker.

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/config"
	"github.com/dbflow-validator/dbflow-validator/internal/domain"
	"github.com/dbflow-validator/dbflow-validator/internal/orchestrator"
)

// capturingHandler is a slog.Handler that collects all log output to a buffer
// regardless of level. Used to assert trace events without touching slog.SetDefault.
type capturingHandler struct {
	buf *bytes.Buffer
}

func newCapturingLogger() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	h := slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(h), buf
}

// makeTraceDeps returns a Deps with a capturing logger, a SQLInput dir with a .sql
// file, an overlayer, and all fake ports wired for a happy-path run.
func makeTraceDeps(t *testing.T) (orchestrator.Deps, config.Config, *bytes.Buffer) {
	t.Helper()

	logger, buf := newCapturingLogger()

	cloneDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(cloneDir, "pom.xml"), []byte(minimalPOM), 0o644); err != nil {
		t.Fatalf("write pom.xml: %v", err)
	}

	// Create a SQLInput dir in the clone for the overlayer.
	sqlInputDest := filepath.Join(cloneDir, "src", "main", "resources", "SQLInput")
	if err := os.MkdirAll(sqlInputDest, 0o700); err != nil {
		t.Fatalf("create SQLInput dest: %v", err)
	}

	// Local SQL source dir (simulates developer working copy).
	srcSQLDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcSQLDir, "N0001_TA_EXAMPLE.sql"), []byte("-- sql"), 0o600); err != nil {
		t.Fatalf("write sql file: %v", err)
	}

	deps := orchestrator.Deps{
		Preflight: &fakePreflight{},
		Cloner:    &fakeCloner{root: cloneDir},
		DBProvider: &fakeDatabaseProvider{
			provider: &fakeContainerProvider{
				coords: domain.ContainerCoords{
					Host:      "127.0.0.1",
					Port:      5432,
					AliasHost: "postgres",
					AliasPort: 5432,
					User:      "u",
					Password:  "p",
					DBName:    "db",
				},
			},
		},
		Patcher:         &fakePatcher{},
		Engine:          &fakeEngineDetector{engine: "postgres"},
		Tags:            &fakeTagResolver{tag: "v1.2.3"},
		Maven:           &fakeMavenRunner{
			syncResult:     domain.StepResult{Status: domain.StepStatusPassed},
			rollbackResult: domain.StepResult{Status: domain.StepStatusPassed},
		},
		ReadinessPolicy: &fastPolicy,
		Overlayer:       &fakeOverlayer{paths: []string{"/fake/dest/a.sql"}},
		// NetworkFactory creates a fake network with a known name so trace assertions
		// can verify the network name appears in log output.
		NetworkFactory: func(_ context.Context) (string, func() error, error) {
			return "dbflow-net-test01", func() error { return nil }, nil
		},
		Logger: logger,
	}

	cfg := config.Config{
		RepoURL:      "https://example.com/repo.git",
		BaseBranch:   "main",
		SQLInputPath: srcSQLDir,
		Token:        domain.NewSecret("supersecrettoken"),
	}

	return deps, cfg, buf
}

// TestTrace_StepBoundariesPresent asserts that step start and end log lines are emitted
// for the known orchestrator steps.
func TestTrace_StepBoundariesPresent(t *testing.T) {
	deps, cfg, buf := makeTraceDeps(t)

	rpt := orchestrator.Run(context.Background(), deps, cfg)
	if rpt.Status != domain.StatusPassed {
		t.Fatalf("expected PASSED run, got %v; steps: %+v", rpt.Status, rpt.Steps)
	}

	output := buf.String()

	// Each step must have a "step.start" and "step.done" log line.
	wantSteps := []string{
		"preflight",
		"clone",
		"engine-guard",
		"overlay",
		"container-start",
		"properties-patch",
		"pre-sync-validate",
		"dbflow:sync",
		"first-tag",
		"dbflow:rollback",
	}

	for _, step := range wantSteps {
		startKey := "step.start"
		doneKey := "step.done"
		if !strings.Contains(output, startKey) || !strings.Contains(output, step) {
			t.Errorf("expected log line with %q and step %q (step start), got log:\n%s", startKey, step, output)
		}
		if !strings.Contains(output, doneKey) || !strings.Contains(output, step) {
			t.Errorf("expected log line with %q and step %q (step done), got log:\n%s", doneKey, step, output)
		}
	}
}

// TestTrace_NetworkNamePresent asserts that the Docker network name appears in the log.
func TestTrace_NetworkNamePresent(t *testing.T) {
	deps, cfg, buf := makeTraceDeps(t)

	orchestrator.Run(context.Background(), deps, cfg)

	if !strings.Contains(buf.String(), "dbflow-net-test01") {
		t.Errorf("expected Docker network name 'dbflow-net-test01' in trace log:\n%s", buf.String())
	}
}

// TestTrace_OverlayFileListPresent asserts that overlay file names appear in the trace log.
func TestTrace_OverlayFileListPresent(t *testing.T) {
	deps, cfg, buf := makeTraceDeps(t)

	orchestrator.Run(context.Background(), deps, cfg)

	output := buf.String()
	if !strings.Contains(output, "N0001_TA_EXAMPLE.sql") {
		t.Errorf("expected overlay file 'N0001_TA_EXAMPLE.sql' in trace log:\n%s", output)
	}
}

// TestTrace_FirstTagPresent asserts that the resolved rollback tag appears in the log.
func TestTrace_FirstTagPresent(t *testing.T) {
	deps, cfg, buf := makeTraceDeps(t)

	orchestrator.Run(context.Background(), deps, cfg)

	if !strings.Contains(buf.String(), "v1.2.3") {
		t.Errorf("expected resolved first-tag 'v1.2.3' in trace log:\n%s", buf.String())
	}
}

// TestTrace_ClonePathPresent asserts that the clone destination path appears in the log.
func TestTrace_ClonePathPresent(t *testing.T) {
	deps, cfg, buf := makeTraceDeps(t)

	// The fakeCloner returns a fixed cloneDir; assert that path appears in the log.
	cloneDir := deps.Cloner.(*fakeCloner).root

	orchestrator.Run(context.Background(), deps, cfg)

	if !strings.Contains(buf.String(), cloneDir) {
		t.Errorf("expected clone path %q in trace log:\n%s", cloneDir, buf.String())
	}
}

// TestTrace_EnginePresent asserts that the resolved engine name appears in the log.
func TestTrace_EnginePresent(t *testing.T) {
	deps, cfg, buf := makeTraceDeps(t)

	orchestrator.Run(context.Background(), deps, cfg)

	if !strings.Contains(buf.String(), "postgres") {
		t.Errorf("expected engine 'postgres' in trace log:\n%s", buf.String())
	}
}

// TestTrace_ConnectionAliasPresent asserts that the DB connection alias
// (AliasHost or Host) appears in the log without credentials.
func TestTrace_ConnectionAliasPresent(t *testing.T) {
	deps, cfg, buf := makeTraceDeps(t)

	orchestrator.Run(context.Background(), deps, cfg)

	output := buf.String()
	// AliasHost "postgres" should appear as the connection alias.
	if !strings.Contains(output, "postgres") {
		t.Errorf("expected connection alias 'postgres' in trace log:\n%s", output)
	}
	// Credentials must NOT appear.
	if strings.Contains(output, "supersecrettoken") {
		t.Errorf("raw token must NOT appear in trace log:\n%s", output)
	}
}

// TestTrace_TokenAbsentFromLog asserts that raw secret tokens are never emitted.
func TestTrace_TokenAbsentFromLog(t *testing.T) {
	deps, cfg, buf := makeTraceDeps(t)

	orchestrator.Run(context.Background(), deps, cfg)

	if strings.Contains(buf.String(), "supersecrettoken") {
		t.Errorf("raw token must NOT appear in trace log:\n%s", buf.String())
	}
}

// TestTrace_DurationInStepDoneLines asserts that step.done lines carry a duration_ms field.
func TestTrace_DurationInStepDoneLines(t *testing.T) {
	deps, cfg, buf := makeTraceDeps(t)

	orchestrator.Run(context.Background(), deps, cfg)

	output := buf.String()
	// At least one step.done line must carry duration_ms.
	if !strings.Contains(output, "step.done") || !strings.Contains(output, "duration_ms") {
		t.Errorf("expected step.done lines with duration_ms in trace log:\n%s", output)
	}
}
