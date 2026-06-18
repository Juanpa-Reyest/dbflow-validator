package orchestrator_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/config"
	"github.com/dbflow-validator/dbflow-validator/internal/domain"
	"github.com/dbflow-validator/dbflow-validator/internal/orchestrator"
)

// TestOrchestrator_FailedStatus_CloneMovedToWorkspace verifies that on FAILED
// the clone is moved to <runDir>/workspace/ and does not remain at its original location.
func TestOrchestrator_FailedStatus_CloneMovedToWorkspace(t *testing.T) {
	runDir := t.TempDir()
	deps, cfg := depsWithRunDir(t, runDir)

	// Force sync failure so the run ends with StatusFailed.
	deps.Maven = &fakeMavenRunner{
		syncResult: domain.StepResult{Status: domain.StepStatusFailed, Error: "BUILD FAILURE"},
	}

	rpt := orchestrator.Run(context.Background(), deps, cfg)

	if rpt.Status != domain.StatusFailed {
		t.Fatalf("expected FAILED, got %v", rpt.Status)
	}

	// The clone should have been moved to <runDir>/workspace/.
	workspacePath := filepath.Join(runDir, "workspace")
	if _, err := os.Stat(workspacePath); os.IsNotExist(err) {
		t.Errorf("expected <runDir>/workspace/ to exist after FAILED run, but it does not")
	}
}

// TestOrchestrator_PassedStatus_CloneRemoved verifies that on PASSED (without
// --keep-workspace) the clone is removed, not moved to <runDir>/workspace/.
func TestOrchestrator_PassedStatus_CloneRemoved(t *testing.T) {
	runDir := t.TempDir()
	deps, cfg := depsWithRunDir(t, runDir)
	deps.KeepWorkspace = false

	rpt := orchestrator.Run(context.Background(), deps, cfg)

	if rpt.Status != domain.StatusPassed {
		t.Fatalf("expected PASSED, got %v; steps: %+v", rpt.Status, rpt.Steps)
	}

	// The workspace dir must NOT exist on success.
	workspacePath := filepath.Join(runDir, "workspace")
	if _, err := os.Stat(workspacePath); !os.IsNotExist(err) {
		t.Errorf("<runDir>/workspace/ must NOT exist after PASSED run without --keep-workspace")
	}
}

// TestOrchestrator_PassedStatus_KeepWorkspace_CloneRetained verifies that on
// PASSED with KeepWorkspace=true, the clone is retained under <runDir>/workspace/.
func TestOrchestrator_PassedStatus_KeepWorkspace_CloneRetained(t *testing.T) {
	runDir := t.TempDir()
	deps, cfg := depsWithRunDir(t, runDir)
	deps.KeepWorkspace = true

	rpt := orchestrator.Run(context.Background(), deps, cfg)

	if rpt.Status != domain.StatusPassed {
		t.Fatalf("expected PASSED, got %v; steps: %+v", rpt.Status, rpt.Steps)
	}

	workspacePath := filepath.Join(runDir, "workspace")
	if _, err := os.Stat(workspacePath); os.IsNotExist(err) {
		t.Errorf("<runDir>/workspace/ must exist after PASSED + KeepWorkspace=true")
	}
}

// TestOrchestrator_ContainerAndNetwork_AlwaysCleanedUp verifies that container and
// network cleanup closures are always invoked regardless of outcome.
// We use invocation counters via the fakeContainerProvider.stopFn.
func TestOrchestrator_ContainerAndNetwork_AlwaysCleanedUp(t *testing.T) {
	runDir := t.TempDir()
	deps, cfg := depsWithRunDir(t, runDir)

	containerStopCount := 0
	networkCleanupCount := 0

	deps.DBProvider = &fakeDatabaseProvider{
		provider: &fakeContainerProvider{
			coords: domain.ContainerCoords{Host: "127.0.0.1", Port: 5432, User: "u", Password: "p", DBName: "db"},
			stopFn: func() error {
				containerStopCount++
				return nil
			},
		},
	}
	deps.NetworkCleanup = func() error {
		networkCleanupCount++
		return nil
	}

	// Force failure to test worst-case cleanup.
	deps.Maven = &fakeMavenRunner{
		syncResult: domain.StepResult{Status: domain.StepStatusFailed, Error: "BUILD FAILURE"},
	}

	orchestrator.Run(context.Background(), deps, cfg)

	if containerStopCount == 0 {
		t.Error("container Stop must be called regardless of outcome")
	}
	if networkCleanupCount == 0 {
		t.Error("network cleanup must be called regardless of outcome")
	}
}

// depsWithRunDir returns orchestrator.Deps and a config suitable for
// status-conditional cleanup tests. The RunDir is wired so the orchestrator
// uses it for workspace disposition decisions.
func depsWithRunDir(t *testing.T, runDir string) (orchestrator.Deps, config.Config) {
	t.Helper()
	deps := happyDeps(t)
	deps.RunDir = runDir
	deps.KeepWorkspace = false
	cfg := testCfg()
	return deps, cfg
}
