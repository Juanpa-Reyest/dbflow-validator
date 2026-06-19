package orchestrator_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
	"github.com/dbflow-validator/dbflow-validator/internal/orchestrator"
)

// snapshotTempCloneDirs returns the set of /tmp/dbflow-clone-* paths that
// currently exist on disk. Used to detect leaks after a failed run.
func snapshotTempCloneDirs(t *testing.T) map[string]struct{} {
	t.Helper()
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", os.TempDir(), err)
	}
	existing := make(map[string]struct{})
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "dbflow-clone-") {
			existing[e.Name()] = struct{}{}
		}
	}
	return existing
}

// TestOrchestrator_CloneFailure_TempDirNotLeaked verifies that when Clone fails,
// the temp directory created by tempCloneDir() is removed — not left behind on disk.
//
// This is a regression test for the bug where tempCloneDir() created
// /tmp/dbflow-clone-* BEFORE clone ran, and a clone failure left it orphaned.
func TestOrchestrator_CloneFailure_TempDirNotLeaked(t *testing.T) {
	before := snapshotTempCloneDirs(t)

	deps := happyDeps(t)
	// Inject a cloner that always fails — simulates git clone returning an error.
	deps.Cloner = &fakeCloner{err: errors.New("repository not found")}

	cfg := testCfg()
	// SQLInputPath is empty in testCfg, which skips the input-check guard,
	// so the run proceeds to the clone step and hits the injected error.
	rpt := orchestrator.Run(context.Background(), deps, cfg)

	if rpt.Status != domain.StatusFailed {
		t.Fatalf("expected FAILED run, got %v", rpt.Status)
	}

	// After the run, no new dbflow-clone-* directories should exist.
	after := snapshotTempCloneDirs(t)
	var leaked []string
	for name := range after {
		if _, existed := before[name]; !existed {
			leaked = append(leaked, name)
		}
	}
	if len(leaked) > 0 {
		t.Errorf("clone failure leaked %d temp dir(s) under %s: %v",
			len(leaked), os.TempDir(), leaked)
		// Best-effort cleanup so subsequent test runs start clean.
		for _, name := range leaked {
			_ = os.RemoveAll(os.TempDir() + "/" + name)
		}
	}
}
