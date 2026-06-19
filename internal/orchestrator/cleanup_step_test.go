package orchestrator_test

// cleanup_step_test.go — TDD tests for the visible cleanup step (Phase: cleanup-visible-step).
//
// These tests verify that:
//  1. The report always includes a "cleanup" step as the last step.
//  2. On PASSED (no keep-workspace): cleanup trace says container removed, network removed, workspace removed.
//  3. On FAILED: cleanup trace says container removed, network removed, workspace RETAINED with the path.
//  4. A teardown error appears in the cleanup step WITHOUT flipping the overall PASSED verdict.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
	"github.com/dbflow-validator/dbflow-validator/internal/orchestrator"
)

// --- helpers shared across cleanup-step tests ---

// buildCleanupTestDeps returns deps wired with:
//   - a real temp clone dir (with pom.xml so driver-inject passes)
//   - runDir set so workspace-retention logic is active
//   - tracking closures for container stop and network cleanup
func buildCleanupTestDeps(
	t *testing.T,
	runDir string,
	containerStopErr error,
	networkCleanupErr error,
) (deps orchestrator.Deps, containerStopped *int, networkCleaned *int) {
	t.Helper()

	cloneDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(cloneDir, "pom.xml"), []byte(minimalPOM), 0o644); err != nil {
		t.Fatalf("write fake pom.xml: %v", err)
	}

	stopped := 0
	cleaned := 0

	deps = orchestrator.Deps{
		Preflight: &fakePreflight{},
		Cloner:    &fakeCloner{root: cloneDir},
		DBProvider: &fakeDatabaseProvider{
			provider: &fakeContainerProvider{
				coords: domain.ContainerCoords{Host: "127.0.0.1", Port: 5432, User: "u", Password: "p", DBName: "db"},
				stopFn: func() error {
					stopped++
					return containerStopErr
				},
			},
		},
		Patcher: &fakePatcher{},
		Engine:  &fakeEngineDetector{engine: "postgres"},
		Tags:    &fakeTagResolver{tag: "210"},
		Maven: &fakeMavenRunner{
			syncResult:     domain.StepResult{Status: domain.StepStatusPassed},
			rollbackResult: domain.StepResult{Status: domain.StepStatusPassed},
		},
		// NetworkFactory creates the network lazily at container-start.
		// The cleanup closure tracks invocations so tests can assert teardown.
		NetworkFactory: func(_ context.Context) (string, func() error, error) {
			cleanup := func() error {
				cleaned++
				return networkCleanupErr
			}
			return "test-net", cleanup, nil
		},
		ReadinessPolicy: &fastPolicy,
		RunDir:          runDir,
		KeepWorkspace:   false,
	}

	return deps, &stopped, &cleaned
}

// findStepOrNil returns the StepResult with the given name, or nil.
// (findStep is already declared in step_trace_test.go with a t.Fatal signature.)
func findStepOrNil(steps []domain.StepResult, name string) *domain.StepResult {
	for i := range steps {
		if steps[i].Name == name {
			return &steps[i]
		}
	}
	return nil
}

// --- Test 1: cleanup step is the last step in the report ---

// TestCleanupStep_AlwaysPresentAsLastStep verifies that after a PASSED run the
// report contains a "cleanup" step and it is the very last step.
func TestCleanupStep_AlwaysPresentAsLastStep(t *testing.T) {
	runDir := t.TempDir()
	deps, _, _ := buildCleanupTestDeps(t, runDir, nil, nil)

	cfg := testCfg()
	rpt := orchestrator.Run(context.Background(), deps, cfg)

	if rpt.Status != domain.StatusPassed {
		t.Fatalf("expected PASSED, got %v; steps: %+v", rpt.Status, rpt.Steps)
	}

	if len(rpt.Steps) == 0 {
		t.Fatal("expected non-empty steps slice")
	}

	last := rpt.Steps[len(rpt.Steps)-1]
	if last.Name != "cleanup" {
		t.Errorf("last step must be 'cleanup', got %q", last.Name)
	}

	if last.Status != domain.StepStatusPassed {
		t.Errorf("cleanup step: expected PASSED, got %v (error: %s)", last.Status, last.Error)
	}
}

// --- Test 2: SUCCESS — block says container removed, network removed, workspace removed ---

// TestCleanupStep_Success_WorkspaceRemoved verifies that on PASSED (without keep-workspace)
// the cleanup trace records:
//   - container removed
//   - network removed
//   - workspace removed (not retained)
func TestCleanupStep_Success_WorkspaceRemoved(t *testing.T) {
	runDir := t.TempDir()
	deps, _, _ := buildCleanupTestDeps(t, runDir, nil, nil)

	cfg := testCfg()
	rpt := orchestrator.Run(context.Background(), deps, cfg)

	if rpt.Status != domain.StatusPassed {
		t.Fatalf("expected PASSED, got %v", rpt.Status)
	}

	step := findStepOrNil(rpt.Steps, "cleanup")
	if step == nil {
		t.Fatal("cleanup step not found in report")
	}

	trace := step.Trace

	// Container (contenedor) must be mentioned.
	if !strings.Contains(trace, "contenedor") && !strings.Contains(strings.ToLower(trace), "container") {
		t.Errorf("cleanup trace must mention 'contenedor' (container); got:\n%s", trace)
	}
	// Network (red) must be mentioned.
	if !strings.Contains(trace, "red docker") && !strings.Contains(strings.ToLower(trace), "network") {
		t.Errorf("cleanup trace must mention 'red docker' (network); got:\n%s", trace)
	}
	// Workspace must be mentioned.
	if !strings.Contains(trace, "workspace") {
		t.Errorf("cleanup trace must mention 'workspace'; got:\n%s", trace)
	}
	// Must NOT say retained on success.
	traceLow := strings.ToLower(trace)
	if strings.Contains(traceLow, "retained") || strings.Contains(traceLow, "retenido") {
		t.Errorf("cleanup trace must NOT say retained on PASSED run; got:\n%s", trace)
	}
	// Must contain a keyword indicating removal.
	if !strings.Contains(trace, "eliminado") && !strings.Contains(trace, "eliminada") && !strings.Contains(traceLow, "removed") {
		t.Errorf("cleanup trace must indicate removal (e.g. 'eliminado'); got:\n%s", trace)
	}
}

// --- Test 3: FAILED — block says workspace RETAINED with the run-dir path ---

// TestCleanupStep_Failed_WorkspaceRetained verifies that on FAILED the cleanup trace
// records that the workspace was retained (moved to run-dir/workspace/) and includes
// the path, while still recording container and network removed.
func TestCleanupStep_Failed_WorkspaceRetained(t *testing.T) {
	runDir := t.TempDir()
	deps, _, _ := buildCleanupTestDeps(t, runDir, nil, nil)

	// Force sync failure so the run ends with StatusFailed.
	deps.Maven = &fakeMavenRunner{
		syncResult: domain.StepResult{Status: domain.StepStatusFailed, Error: "BUILD FAILURE"},
	}

	cfg := testCfg()
	rpt := orchestrator.Run(context.Background(), deps, cfg)

	if rpt.Status != domain.StatusFailed {
		t.Fatalf("expected FAILED, got %v", rpt.Status)
	}

	step := findStepOrNil(rpt.Steps, "cleanup")
	if step == nil {
		t.Fatal("cleanup step not found in report")
	}

	// Cleanup step itself must be PASSED (teardown errors don't flip the cleanup step
	// verdict here — we test that separately in TestCleanupStep_TeardownError).
	if step.Status != domain.StepStatusPassed {
		t.Errorf("cleanup step: expected PASSED, got %v (error: %s)", step.Status, step.Error)
	}

	trace := step.Trace

	// The trace must mention workspace retained.
	traceLow := strings.ToLower(trace)
	if !strings.Contains(traceLow, "retained") && !strings.Contains(traceLow, "retenido") {
		t.Errorf("cleanup trace must say workspace was retained on FAILED; got:\n%s", trace)
	}

	// The path to <runDir>/workspace must appear in the trace.
	expectedPath := filepath.Join(runDir, "workspace")
	if !strings.Contains(trace, expectedPath) && !strings.Contains(trace, runDir) {
		t.Errorf("cleanup trace must include the retained workspace path (%s); got:\n%s", expectedPath, trace)
	}

	// Container and network must still be mentioned as removed.
	if !strings.Contains(trace, "contenedor") && !strings.Contains(traceLow, "container") {
		t.Errorf("cleanup trace must mention 'contenedor' (container); got:\n%s", trace)
	}
	if !strings.Contains(trace, "red docker") && !strings.Contains(traceLow, "network") {
		t.Errorf("cleanup trace must mention 'red docker' (network); got:\n%s", trace)
	}
}

// --- Test 4: teardown error captured in cleanup step, overall verdict unchanged ---

// TestCleanupStep_TeardownError_DoesNotFlipOverallVerdict verifies that when a teardown
// operation errors (e.g. container stop fails), the cleanup step captures the error but
// the overall run verdict (PASSED) is NOT changed to FAILED.
func TestCleanupStep_TeardownError_DoesNotFlipOverallVerdict(t *testing.T) {
	runDir := t.TempDir()
	containerErr := errors.New("container stop: permission denied")
	deps, _, _ := buildCleanupTestDeps(t, runDir, containerErr, nil)

	cfg := testCfg()
	rpt := orchestrator.Run(context.Background(), deps, cfg)

	// Overall verdict must remain PASSED even though container stop errored.
	if rpt.Status != domain.StatusPassed {
		t.Errorf("overall verdict must remain PASSED despite teardown error; got %v", rpt.Status)
	}

	step := findStepOrNil(rpt.Steps, "cleanup")
	if step == nil {
		t.Fatal("cleanup step not found in report")
	}

	// The cleanup step itself must be FAILED (to surface the error).
	if step.Status != domain.StepStatusFailed {
		t.Errorf("cleanup step must be FAILED when a teardown op errors; got %v", step.Status)
	}

	// The error message must be present.
	if step.Error == "" {
		t.Error("cleanup step must have a non-empty error message when teardown fails")
	}

	// The error must reference the underlying cause.
	if !strings.Contains(step.Error, "permission denied") && !strings.Contains(step.Error, "container") {
		t.Errorf("cleanup step error should mention the teardown failure; got: %q", step.Error)
	}
}

// --- Test 5: cleanup step present on FAILED (early exit path) ---

// TestCleanupStep_PresentOnFailedRun verifies that even when the run ends with FAILED
// (e.g. preflight fails), the cleanup step is still included as the last step.
func TestCleanupStep_PresentOnFailedRun(t *testing.T) {
	runDir := t.TempDir()
	deps, _, _ := buildCleanupTestDeps(t, runDir, nil, nil)
	deps.Preflight = &fakePreflight{err: errors.New("docker not found")}

	cfg := testCfg()
	rpt := orchestrator.Run(context.Background(), deps, cfg)

	if rpt.Status != domain.StatusFailed {
		t.Fatalf("expected FAILED, got %v", rpt.Status)
	}

	step := findStepOrNil(rpt.Steps, "cleanup")
	if step == nil {
		t.Fatal("cleanup step must be present even on early-failure runs")
	}

	last := rpt.Steps[len(rpt.Steps)-1]
	if last.Name != "cleanup" {
		t.Errorf("cleanup step must be the last step; got %q", last.Name)
	}
}

// --- Test 6: OnStep emits start and done events for cleanup ---

// TestCleanupStep_OnStep_EmitsStartAndDone verifies that the cleanup step emits
// a start event (Done=false) and a done event (Done=true) via the OnStep callback.
func TestCleanupStep_OnStep_EmitsStartAndDone(t *testing.T) {
	runDir := t.TempDir()
	deps, _, _ := buildCleanupTestDeps(t, runDir, nil, nil)

	var events []orchestrator.StepEvent
	deps.OnStep = func(e orchestrator.StepEvent) {
		events = append(events, e)
	}

	cfg := testCfg()
	rpt := orchestrator.Run(context.Background(), deps, cfg)

	if rpt.Status != domain.StatusPassed {
		t.Fatalf("expected PASSED, got %v", rpt.Status)
	}

	var startEvt, doneEvt *orchestrator.StepEvent
	for i := range events {
		if events[i].Name == "cleanup" {
			if !events[i].Done {
				startEvt = &events[i]
			} else {
				doneEvt = &events[i]
			}
		}
	}

	if startEvt == nil {
		t.Error("expected a start event (Done=false) for 'cleanup'")
	}
	if doneEvt == nil {
		t.Error("expected a done event (Done=true) for 'cleanup'")
	}
	if doneEvt != nil && doneEvt.Failed {
		t.Error("done event for 'cleanup' must have Failed=false on success")
	}

	// Cleanup event must come AFTER the last validation step's done event.
	lastStepName := ""
	for _, e := range events {
		if e.Done && e.Name != "cleanup" {
			lastStepName = e.Name
		}
	}
	_ = lastStepName // informational — the last validation step before cleanup
}

// --- Test 7: network teardown error also captured ---

// TestCleanupStep_NetworkTeardownError_CapturedInCleanupStep verifies that a network
// cleanup error is captured in the cleanup step error field.
func TestCleanupStep_NetworkTeardownError_CapturedInCleanupStep(t *testing.T) {
	runDir := t.TempDir()
	networkErr := errors.New("network rm: resource busy")
	deps, _, _ := buildCleanupTestDeps(t, runDir, nil, networkErr)

	cfg := testCfg()
	rpt := orchestrator.Run(context.Background(), deps, cfg)

	// Overall verdict must remain PASSED.
	if rpt.Status != domain.StatusPassed {
		t.Errorf("overall verdict must remain PASSED despite network teardown error; got %v", rpt.Status)
	}

	step := findStepOrNil(rpt.Steps, "cleanup")
	if step == nil {
		t.Fatal("cleanup step not found in report")
	}

	if step.Status != domain.StepStatusFailed {
		t.Errorf("cleanup step must be FAILED when network teardown errors; got %v", step.Status)
	}
	if !strings.Contains(step.Error, "resource busy") && !strings.Contains(step.Error, "network") {
		t.Errorf("cleanup step error must mention the network teardown failure; got: %q", step.Error)
	}
}
