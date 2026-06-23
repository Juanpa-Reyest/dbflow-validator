package rulesvalidator_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/rulesvalidator"
)

// ---------------------------------------------------------------------------
// Unit tests — no Docker, no JAR, use a fake container runner
// ---------------------------------------------------------------------------

// fakeRunner is a test double for the container execution boundary.
// It simulates the JAR container by returning canned output.
type fakeRunner struct {
	output string
	err    error
}

func (f *fakeRunner) RunValidator(
	_ context.Context,
	req rulesvalidator.ValidatorContainerRequest,
) (string, error) {
	return f.output, f.err
}

func makeValidatorCloneRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	rulesetDir := filepath.Join(root, "src", "main", "resources", "Validator", "RulesContracts")
	if err := os.MkdirAll(rulesetDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rulesetDir, "validation-rules.yaml"), []byte("rules: []"), 0o600); err != nil {
		t.Fatalf("write ruleset: %v", err)
	}
	sqlInputDir := filepath.Join(root, "src", "main", "resources", "SQLInput")
	if err := os.MkdirAll(sqlInputDir, 0o700); err != nil {
		t.Fatalf("mkdir SQLInput: %v", err)
	}
	return root
}

func TestContainerValidator_Pass_ReturnsNil(t *testing.T) {
	passLog := fixture(t, "pass.log")
	runner := &fakeRunner{output: passLog}

	v := rulesvalidator.New(
		"maven:3.9-eclipse-temurin-21",
		"/cache/validator.jar",
		1000, 1000,
		runner,
	)

	cloneRoot := makeValidatorCloneRoot(t)
	err := v.ValidatePreSync(context.Background(), cloneRoot)
	if err != nil {
		t.Errorf("ValidatePreSync(PASS): expected nil, got %v", err)
	}
}

func TestContainerValidator_Fail_ReturnsError(t *testing.T) {
	failLog := fixture(t, "fail.log")
	runner := &fakeRunner{output: failLog}

	v := rulesvalidator.New(
		"maven:3.9-eclipse-temurin-21",
		"/cache/validator.jar",
		1000, 1000,
		runner,
	)

	cloneRoot := makeValidatorCloneRoot(t)
	err := v.ValidatePreSync(context.Background(), cloneRoot)
	if err == nil {
		t.Error("ValidatePreSync(FAIL): expected non-nil error")
	}
}

func TestContainerValidator_MissingRuleset_ReturnsErrRulesetMissing(t *testing.T) {
	runner := &fakeRunner{}

	v := rulesvalidator.New(
		"maven:3.9-eclipse-temurin-21",
		"/cache/validator.jar",
		1000, 1000,
		runner,
	)

	// cloneRoot without the ruleset YAML.
	cloneRoot := t.TempDir()
	err := v.ValidatePreSync(context.Background(), cloneRoot)
	if err == nil {
		t.Fatal("expected error for missing ruleset")
	}
	if !strings.Contains(err.Error(), "ruleset") && !strings.Contains(err.Error(), "not found") {
		t.Errorf("error message should mention ruleset; got: %v", err)
	}
}

func TestContainerValidator_NoJSONInOutput_ReturnsError(t *testing.T) {
	runner := &fakeRunner{output: "just noise, no JSON here"}

	v := rulesvalidator.New(
		"maven:3.9-eclipse-temurin-21",
		"/cache/validator.jar",
		1000, 1000,
		runner,
	)

	cloneRoot := makeValidatorCloneRoot(t)
	err := v.ValidatePreSync(context.Background(), cloneRoot)
	if err == nil {
		t.Error("expected error when no JSON found in output")
	}
}

func TestContainerValidator_ImplementsPreSyncValidator(t *testing.T) {
	// This test verifies the interface compliance via compile-time assertion.
	// The actual assertion is in validator.go (var _ domain.PreSyncValidator = ...).
	// This test just ensures the type can be constructed and is non-nil.
	runner := &fakeRunner{}
	v := rulesvalidator.New(
		"maven:3.9-eclipse-temurin-21",
		"/cache/validator.jar",
		0, 0,
		runner,
	)
	if v == nil {
		t.Fatal("New() returned nil")
	}
}

// ---------------------------------------------------------------------------
// Integration test — Docker, real JAR
// ---------------------------------------------------------------------------

func TestContainerValidator_Integration_Pass(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test requires Docker and real JAR; skipped with -short")
	}

	jarPath := findRealJAR(t)
	cloneRoot := findRealCloneRoot(t)

	v := rulesvalidator.New(
		"maven:3.9-eclipse-temurin-21",
		jarPath,
		os.Getuid(), os.Getgid(),
		nil, // nil runner → use real Docker
	)

	err := v.ValidatePreSync(context.Background(), cloneRoot)
	if err != nil {
		t.Errorf("Integration PASS: expected nil error, got: %v", err)
	}
}

func TestContainerValidator_Integration_Fail(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test requires Docker and real JAR; skipped with -short")
	}

	jarPath := findRealJAR(t)

	// Create a temp clone root with a rule-violating SQL file.
	cloneRoot := makeValidatorCloneRoot(t)
	sqlFile := filepath.Join(cloneRoot, "src", "main", "resources", "SQLInput", "N0001_DDL_TBL_BAD.sql")
	badSQL := `CREATE TABLE bad_table_name (
    id integer,
    Name varchar(100),
    DESCRIPTION text
);`
	if err := os.WriteFile(sqlFile, []byte(badSQL), 0o600); err != nil {
		t.Fatalf("write bad SQL: %v", err)
	}

	// Replace the ruleset with the real one from the script-validator dir.
	realRuleset := "/home/juanpabloreyestorres/Documentos/Documentos/FTT/files/Sistema golf/db-scripts/v2/ai/workspaces/ws-ai-dbflow-validator/script-validator/validation-rules.yaml"
	destRuleset := filepath.Join(cloneRoot, "src", "main", "resources", "Validator", "RulesContracts", "validation-rules.yaml")
	data, err := os.ReadFile(realRuleset)
	if err != nil {
		t.Skipf("real ruleset not found at %s; skipping: %v", realRuleset, err)
	}
	if err := os.WriteFile(destRuleset, data, 0o600); err != nil {
		t.Fatalf("write ruleset: %v", err)
	}

	v := rulesvalidator.New(
		"maven:3.9-eclipse-temurin-21",
		jarPath,
		os.Getuid(), os.Getgid(),
		nil,
	)

	err = v.ValidatePreSync(context.Background(), cloneRoot)
	if err == nil {
		t.Error("Integration FAIL: expected non-nil error for violating SQL")
	}
}

// findRealJAR returns the path to the embedded JAR (extracted to a temp cache).
func findRealJAR(t *testing.T) string {
	t.Helper()
	// The real jar lives in the embedvalidator package.
	// For integration tests, extract it to a temp dir.
	import_path := filepath.Join(
		"/home/juanpabloreyestorres/Documentos/Documentos/FTT/files/Sistema golf/db-scripts/v2/ai/workspaces/ws-ai-dbflow-validator/dbflow-validator",
		"internal", "embedvalidator", "jar", "library-script-validator-postgresql.jar",
	)
	if _, err := os.Stat(import_path); err != nil {
		t.Skipf("real JAR not found at %s; skipping integration test", import_path)
	}
	return import_path
}

// findRealCloneRoot returns the path to the real repo for integration PASS tests.
func findRealCloneRoot(t *testing.T) string {
	t.Helper()
	cloneRoot := "/home/juanpabloreyestorres/Documentos/Documentos/FTT/Repos/gs-github/albatros/db-artifacts-scgolfcore"
	rulesetPath := filepath.Join(cloneRoot, "src", "main", "resources", "Validator", "RulesContracts", "validation-rules.yaml")
	if _, err := os.Stat(rulesetPath); err != nil {
		t.Skipf("real clone root not found at %s; skipping", cloneRoot)
	}
	return cloneRoot
}
