package rulesvalidator_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/rulesvalidator"
)

// makeCloneRoot builds a minimal clone-root directory tree for locate tests.
func makeCloneRoot(t *testing.T, withRuleset bool) string {
	t.Helper()
	root := t.TempDir()

	if withRuleset {
		rulesetDir := filepath.Join(root, "src", "main", "resources", "Validator", "RulesContracts")
		if err := os.MkdirAll(rulesetDir, 0o700); err != nil {
			t.Fatalf("mkdir ruleset dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(rulesetDir, "validation-rules.yaml"), []byte("rules: []"), 0o600); err != nil {
			t.Fatalf("write ruleset: %v", err)
		}
	}

	sqlInputDir := filepath.Join(root, "src", "main", "resources", "SQLInput")
	if err := os.MkdirAll(sqlInputDir, 0o700); err != nil {
		t.Fatalf("mkdir SQLInput: %v", err)
	}

	return root
}

func TestLocate_RulesetFound_ReturnsPaths(t *testing.T) {
	root := makeCloneRoot(t, true)
	paths, err := rulesvalidator.Locate(root)
	if err != nil {
		t.Fatalf("Locate() unexpected error: %v", err)
	}
	if paths.RulesetPath == "" {
		t.Error("RulesetPath must not be empty")
	}
	if paths.SQLInputPath == "" {
		t.Error("SQLInputPath must not be empty")
	}
}

func TestLocate_RulesetFound_PathsContainCloneRoot(t *testing.T) {
	root := makeCloneRoot(t, true)
	paths, err := rulesvalidator.Locate(root)
	if err != nil {
		t.Fatalf("Locate() unexpected error: %v", err)
	}
	// Both paths must be under cloneRoot.
	rulesetRel, relErr := filepath.Rel(root, paths.RulesetPath)
	if relErr != nil || rulesetRel == "" {
		t.Errorf("RulesetPath %q is not under cloneRoot %q", paths.RulesetPath, root)
	}
	sqlRel, relErr2 := filepath.Rel(root, paths.SQLInputPath)
	if relErr2 != nil || sqlRel == "" {
		t.Errorf("SQLInputPath %q is not under cloneRoot %q", paths.SQLInputPath, root)
	}
}

func TestLocate_RulesetMissing_ReturnsErrRulesetMissing(t *testing.T) {
	root := makeCloneRoot(t, false)
	_, err := rulesvalidator.Locate(root)
	if !errors.Is(err, rulesvalidator.ErrRulesetMissing) {
		t.Errorf("expected ErrRulesetMissing, got %v", err)
	}
}

func TestLocate_RulesetMissing_ErrorContainsExpectedPath(t *testing.T) {
	root := makeCloneRoot(t, false)
	_, err := rulesvalidator.Locate(root)
	if err == nil {
		t.Fatal("expected error")
	}
	// The error message must name the expected path.
	if err.Error() == "" {
		t.Error("error message must not be empty")
	}
}

func TestLocate_SQLInputPath_IsFixed(t *testing.T) {
	root := makeCloneRoot(t, true)
	paths, err := rulesvalidator.Locate(root)
	if err != nil {
		t.Fatalf("Locate() unexpected error: %v", err)
	}
	want := filepath.Join(root, "src", "main", "resources", "SQLInput")
	if paths.SQLInputPath != want {
		t.Errorf("SQLInputPath = %q, want %q", paths.SQLInputPath, want)
	}
}

func TestLocate_RulesetPath_IsFixed(t *testing.T) {
	root := makeCloneRoot(t, true)
	paths, err := rulesvalidator.Locate(root)
	if err != nil {
		t.Fatalf("Locate() unexpected error: %v", err)
	}
	want := filepath.Join(root, "src", "main", "resources", "Validator", "RulesContracts", "validation-rules.yaml")
	if paths.RulesetPath != want {
		t.Errorf("RulesetPath = %q, want %q", paths.RulesetPath, want)
	}
}
