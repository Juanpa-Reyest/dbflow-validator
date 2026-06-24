package rulesvalidator_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/rulesvalidator"
)

// ---------------------------------------------------------------------------
// Unit tests — no Docker, no JAR
// ---------------------------------------------------------------------------

// fakeRunner is a minimal ContainerRunner test double.
// It returns a fixed error (or nil) and does NOT write any report file.
// Used to test paths that do not reach the report-reading step
// (e.g. container execution failure, missing ruleset).
type fakeRunner struct {
	err error
}

func (f *fakeRunner) RunValidator(
	_ context.Context,
	_ rulesvalidator.ValidatorContainerRequest,
) (string, error) {
	return "", f.err
}

// makeValidatorCloneRoot creates a minimal clone root with the required directory
// structure but WITHOUT the outputReport dir (the JAR creates it at runtime).
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

// ---------------------------------------------------------------------------
// logCapturingRunner — test double for ValidatorOut capture tests
// ---------------------------------------------------------------------------

// logCapturingRunner simulates the JAR: it writes a JSON report to disk AND
// returns a fixed log string, letting tests assert what lands in ValidatorOut.
type logCapturingRunner struct {
	reportJSON string
	logOutput  string
	err        error
}

func (r *logCapturingRunner) RunValidator(
	_ context.Context,
	req rulesvalidator.ValidatorContainerRequest,
) (string, error) {
	if r.err != nil {
		return r.logOutput, r.err
	}
	// Write the report file, mirroring what the real JAR does.
	// Derive cloneRoot from the typed mount targeting /work.
	var cloneRoot string
	for _, m := range req.Mounts {
		if m.Target == "/work" {
			cloneRoot = m.Source
			break
		}
	}
	if cloneRoot == "" {
		return r.logOutput, errors.New("logCapturingRunner: no /work mount found")
	}
	reportPath := rulesvalidator.ReportPath(cloneRoot)
	if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
		return r.logOutput, err
	}
	if err := os.WriteFile(reportPath, []byte(r.reportJSON), 0o644); err != nil {
		return r.logOutput, err
	}
	return r.logOutput, nil
}

// ---------------------------------------------------------------------------
// ValidatorOut capture tests (Change 2)
// ---------------------------------------------------------------------------

// TestContainerValidator_ValidatorOut_WritesOutputOnPass asserts that the JAR
// log output is written to ValidatorOut even on a passing run.
func TestContainerValidator_ValidatorOut_WritesOutputOnPass(t *testing.T) {
	const jarLog = "[INFO] Validator started\n[INFO] No violations found.\n"
	passJSON := fixtureJSON(t, "pass_report.json")

	runner := &logCapturingRunner{reportJSON: passJSON, logOutput: jarLog}

	var buf bytes.Buffer
	v := rulesvalidator.New(
		"maven:3.9-eclipse-temurin-21",
		"/cache/validator.jar",
		1000, 1000,
		runner,
		rulesvalidator.WithValidatorOut(&buf),
	)

	cloneRoot := makeValidatorCloneRootWithOutputReportDir(t)
	if _, err := v.ValidatePreSync(context.Background(), cloneRoot); err != nil {
		t.Fatalf("ValidatePreSync: unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "[INFO] Validator started") {
		t.Errorf("ValidatorOut missing JAR log on PASS; got: %q", buf.String())
	}
}

// TestContainerValidator_ValidatorOut_WritesOutputOnFail asserts that the JAR
// log output is captured in ValidatorOut even when the gate fails (write-then-decide).
func TestContainerValidator_ValidatorOut_WritesOutputOnFail(t *testing.T) {
	const jarLog = "[WARN] Violation found in N0001_DDL_TBL_BAD.sql\n"
	failJSON := fixtureJSON(t, "fail_report.json")

	runner := &logCapturingRunner{reportJSON: failJSON, logOutput: jarLog}

	var buf bytes.Buffer
	v := rulesvalidator.New(
		"maven:3.9-eclipse-temurin-21",
		"/cache/validator.jar",
		1000, 1000,
		runner,
		rulesvalidator.WithValidatorOut(&buf),
	)

	cloneRoot := makeValidatorCloneRootWithOutputReportDir(t)
	_, err := v.ValidatePreSync(context.Background(), cloneRoot)
	if err == nil {
		t.Fatal("ValidatePreSync(FAIL): expected error, got nil")
	}
	// The output must have been written BEFORE the gate decision.
	if !strings.Contains(buf.String(), "[WARN] Violation found") {
		t.Errorf("ValidatorOut missing JAR log on FAIL; got: %q", buf.String())
	}
}

// TestContainerValidator_ValidatorOut_NilWriter_DoesNotPanic asserts that nil
// ValidatorOut (the default) leaves existing behaviour unchanged.
func TestContainerValidator_ValidatorOut_NilWriter_DoesNotPanic(t *testing.T) {
	passJSON := fixtureJSON(t, "pass_report.json")
	runner := &logCapturingRunner{reportJSON: passJSON, logOutput: "some log\n"}

	// No WithValidatorOut — default nil writer.
	v := rulesvalidator.New(
		"maven:3.9-eclipse-temurin-21",
		"/cache/validator.jar",
		1000, 1000,
		runner,
	)

	cloneRoot := makeValidatorCloneRootWithOutputReportDir(t)
	if _, err := v.ValidatePreSync(context.Background(), cloneRoot); err != nil {
		t.Errorf("ValidatePreSync with nil ValidatorOut: unexpected error: %v", err)
	}
}

// Pass/Fail/MissingReport unit tests for the file-based flow are in report_file_test.go
// (TestContainerValidator_FileBasedFlow_*). They use fileWritingRunner which simulates
// the JAR writing the JSON report to disk.

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
	_, err := v.ValidatePreSync(context.Background(), cloneRoot)
	if err == nil {
		t.Fatal("expected error for missing ruleset")
	}
	if !strings.Contains(err.Error(), "ruleset") && !strings.Contains(err.Error(), "not found") {
		t.Errorf("error message should mention ruleset; got: %v", err)
	}
}

func TestContainerValidator_ContainerError_ReturnsError(t *testing.T) {
	runner := &fakeRunner{err: os.ErrNotExist} // simulate Docker failure

	v := rulesvalidator.New(
		"maven:3.9-eclipse-temurin-21",
		"/cache/validator.jar",
		1000, 1000,
		runner,
	)

	cloneRoot := makeValidatorCloneRoot(t)
	_, err := v.ValidatePreSync(context.Background(), cloneRoot)
	if err == nil {
		t.Error("expected error when container execution fails")
	}
}

func TestContainerValidator_ImplementsPreSyncValidator(t *testing.T) {
	// The compile-time assertion is in validator.go (var _ domain.PreSyncValidator = ...).
	// This test ensures the type can be constructed and is non-nil.
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
// ValidatePreSync return value — output string
// ---------------------------------------------------------------------------

// TestContainerValidator_ValidatePreSync_PassReturnsOutput asserts that on a
// passing run the returned output string contains the container log.
func TestContainerValidator_ValidatePreSync_PassReturnsOutput(t *testing.T) {
	const jarLog = "[INFO] Validator started\n[INFO] No violations found.\n"
	passJSON := fixtureJSON(t, "pass_report.json")

	runner := &logCapturingRunner{reportJSON: passJSON, logOutput: jarLog}

	v := rulesvalidator.New(
		"maven:3.9-eclipse-temurin-21",
		"/cache/validator.jar",
		1000, 1000,
		runner,
	)

	cloneRoot := makeValidatorCloneRootWithOutputReportDir(t)
	output, err := v.ValidatePreSync(context.Background(), cloneRoot)
	if err != nil {
		t.Fatalf("ValidatePreSync: unexpected error: %v", err)
	}
	if !strings.Contains(output, "[INFO] Validator started") {
		t.Errorf("ValidatePreSync(PASS) output missing JAR log; got: %q", output)
	}
}

// TestContainerValidator_ValidatePreSync_FailReturnsOutputAndError asserts that
// on a failing run the error is non-nil AND the returned output still contains
// the container log (so failure evidence is always surfaced).
func TestContainerValidator_ValidatePreSync_FailReturnsOutputAndError(t *testing.T) {
	const jarLog = "[WARN] Violation found in N0001_DDL_TBL_BAD.sql\n"
	failJSON := fixtureJSON(t, "fail_report.json")

	runner := &logCapturingRunner{reportJSON: failJSON, logOutput: jarLog}

	var buf bytes.Buffer
	v := rulesvalidator.New(
		"maven:3.9-eclipse-temurin-21",
		"/cache/validator.jar",
		1000, 1000,
		runner,
		rulesvalidator.WithValidatorOut(&buf),
	)

	cloneRoot := makeValidatorCloneRootWithOutputReportDir(t)
	output, err := v.ValidatePreSync(context.Background(), cloneRoot)
	if err == nil {
		t.Fatal("ValidatePreSync(FAIL): expected error, got nil")
	}
	if !strings.Contains(output, "[WARN] Violation found") {
		t.Errorf("ValidatePreSync(FAIL) output missing JAR log; got: %q", output)
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

	_, err := v.ValidatePreSync(context.Background(), cloneRoot)
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

	_, err = v.ValidatePreSync(context.Background(), cloneRoot)
	if err == nil {
		t.Error("Integration FAIL: expected non-nil error for violating SQL")
	}
}

// findRealJAR returns the path to the embedded JAR.
func findRealJAR(t *testing.T) string {
	t.Helper()
	import_path := filepath.Join(
		"/home/juanpabloreyestorres/Documentos/Documentos/FTT/files/Sistema golf/db-scripts/v2/ai/workspaces/ws-ai-dbflow-validator/dbflow-validator",
		"internal", "embedvalidator", "jar", "library-script-validator-postgresql.jar",
	)
	if _, err := os.Stat(import_path); err != nil {
		t.Skipf("real JAR not found at %s; skipping integration test", import_path)
	}
	return import_path
}

// findRealCloneRoot builds a THROWAWAY clone-root copy (t.TempDir) seeded with the
// real repo's ruleset and a DETERMINISTIC no-violation SQL file. The integration
// PASS test thus runs against the real rules + the real JAR, but its result does
// NOT depend on the live repo's SQLInput (which drifts as the dev edits SQL and
// can introduce violations that would flip a PASS test to FAIL). The JAR's -output
// writes into this temp dir — it must NEVER write into the developer's actual repo
// (this mirrors the ephemeral clone used in production, the only place the binary writes).
func findRealCloneRoot(t *testing.T) string {
	t.Helper()
	realRepo := "/home/juanpabloreyestorres/Documentos/Documentos/FTT/Repos/gs-github/albatros/db-artifacts-scgolfcore"
	realRules := filepath.Join(realRepo, "src", "main", "resources", "Validator", "RulesContracts", "validation-rules.yaml")
	if _, err := os.Stat(realRules); err != nil {
		t.Skipf("real ruleset not found at %s; skipping", realRules)
	}

	root := t.TempDir()
	rulesetDir := filepath.Join(root, "src", "main", "resources", "Validator", "RulesContracts")
	if err := os.MkdirAll(rulesetDir, 0o755); err != nil {
		t.Fatalf("mkdir ruleset: %v", err)
	}
	copyFileForTest(t, realRules, filepath.Join(rulesetDir, "validation-rules.yaml"))

	// The -output target dir (the JAR creates report/ subdirs under it).
	if err := os.MkdirAll(filepath.Join(root, "src", "main", "resources", "Validator", "outputReport"), 0o755); err != nil {
		t.Fatalf("mkdir outputReport: %v", err)
	}

	// Seed a DETERMINISTIC, no-violation SQL file so this PASS test stays green
	// regardless of the live repo's SQLInput. A comment-only script matches no
	// rule's apply_if, so the validator reports no applicable rules → PASS.
	dstSQL := filepath.Join(root, "src", "main", "resources", "SQLInput")
	if err := os.MkdirAll(dstSQL, 0o755); err != nil {
		t.Fatalf("mkdir SQLInput: %v", err)
	}
	noop := "-- deterministic no-op script for the integration PASS test; matches no rule\n"
	if err := os.WriteFile(filepath.Join(dstSQL, "N0000_NOOP.sql"), []byte(noop), 0o600); err != nil {
		t.Fatalf("write noop sql: %v", err)
	}
	return root
}

// copyFileForTest copies a single file (test helper).
func copyFileForTest(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
}
