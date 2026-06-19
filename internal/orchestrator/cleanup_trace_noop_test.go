package orchestrator_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
	"github.com/dbflow-validator/dbflow-validator/internal/orchestrator"
)

// TestCleanupTrace_EarlyFailure_ContainerNetworkShowNA verifies that when the run
// fails before the container or network are started (e.g. clone fails), the cleanup
// trace reports n/a for both resources — not "eliminado/eliminada" which would be
// misleading since those resources were never created.
func TestCleanupTrace_EarlyFailure_ContainerNetworkShowNA(t *testing.T) {
	deps := happyDeps(t)
	// Clone fails — container and network are never started.
	deps.Cloner = &fakeCloner{err: errors.New("auth failed")}

	cfg := testCfg()
	rpt := orchestrator.Run(context.Background(), deps, cfg)

	if rpt.Status != domain.StatusFailed {
		t.Fatalf("expected FAILED, got %v", rpt.Status)
	}

	step := findStepOrNil(rpt.Steps, "cleanup")
	if step == nil {
		t.Fatal("cleanup step not found in report")
	}

	trace := step.Trace

	// Container must NOT say "eliminado" — it was never created.
	if strings.Contains(trace, "eliminado") {
		t.Errorf("cleanup trace must not say 'eliminado' for container that was never started;\ngot:\n%s", trace)
	}
	// Network must NOT say "eliminada" — it was never registered.
	if strings.Contains(trace, "eliminada") {
		t.Errorf("cleanup trace must not say 'eliminada' for network that was never started;\ngot:\n%s", trace)
	}
	// Both must show n/a or similar.
	if !strings.Contains(trace, "n/a") && !strings.Contains(strings.ToLower(trace), "not created") &&
		!strings.Contains(strings.ToLower(trace), "no se cre") {
		t.Errorf("cleanup trace must indicate container/network were not created (e.g. n/a);\ngot:\n%s", trace)
	}
}

// TestCleanupTrace_FullRun_ContainerNetworkShowEliminado verifies that on a full
// successful run (container and network both registered), the cleanup trace shows
// the resources as eliminated/removed — not n/a.
func TestCleanupTrace_FullRun_ContainerNetworkShowEliminado(t *testing.T) {
	runDir := t.TempDir()
	deps, _, _ := buildCleanupTestDeps(t, runDir, nil, nil)
	// buildCleanupTestDeps wires a real NetworkCleanup and a container with stopFn.

	cfg := testCfg()
	rpt := orchestrator.Run(context.Background(), deps, cfg)

	if rpt.Status != domain.StatusPassed {
		t.Fatalf("expected PASSED, got %v; steps: %+v", rpt.Status, rpt.Steps)
	}

	step := findStepOrNil(rpt.Steps, "cleanup")
	if step == nil {
		t.Fatal("cleanup step not found in report")
	}

	trace := step.Trace

	// Container must say "eliminado" — it was started and then stopped.
	if !strings.Contains(trace, "eliminado") {
		t.Errorf("cleanup trace must say 'eliminado' for container that was started;\ngot:\n%s", trace)
	}
	// Network must say "eliminada" — it was registered and torn down.
	if !strings.Contains(trace, "eliminada") {
		t.Errorf("cleanup trace must say 'eliminada' for network that was registered;\ngot:\n%s", trace)
	}
}
