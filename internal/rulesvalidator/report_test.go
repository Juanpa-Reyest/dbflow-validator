package rulesvalidator_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/rulesvalidator"
)

// fixture loads a testdata file and panics if it cannot be read.
func fixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("load fixture %q: %v", name, err)
	}
	return string(data)
}

// ---------------------------------------------------------------------------
// ExtractReport
// ---------------------------------------------------------------------------

func TestExtractReport_Pass_ReturnsPassStatus(t *testing.T) {
	log := fixture(t, "pass.log")
	rpt, err := rulesvalidator.ExtractReport(log)
	if err != nil {
		t.Fatalf("ExtractReport(pass.log): unexpected error: %v", err)
	}
	if rpt.GlobalSummary.Status != "PASS" {
		t.Errorf("status = %q, want PASS", rpt.GlobalSummary.Status)
	}
}

func TestExtractReport_Pass_ScoreIs100(t *testing.T) {
	log := fixture(t, "pass.log")
	rpt, err := rulesvalidator.ExtractReport(log)
	if err != nil {
		t.Fatalf("ExtractReport(pass.log): unexpected error: %v", err)
	}
	if rpt.GlobalSummary.Score != 100.0 {
		t.Errorf("score = %v, want 100.0", rpt.GlobalSummary.Score)
	}
}

func TestExtractReport_Fail_ReturnsFailStatus(t *testing.T) {
	log := fixture(t, "fail.log")
	rpt, err := rulesvalidator.ExtractReport(log)
	if err != nil {
		t.Fatalf("ExtractReport(fail.log): unexpected error: %v", err)
	}
	if rpt.GlobalSummary.Status != "FAIL" {
		t.Errorf("status = %q, want FAIL", rpt.GlobalSummary.Status)
	}
}

func TestExtractReport_Fail_HasViolations(t *testing.T) {
	log := fixture(t, "fail.log")
	rpt, err := rulesvalidator.ExtractReport(log)
	if err != nil {
		t.Fatalf("ExtractReport(fail.log): unexpected error: %v", err)
	}
	sev := rpt.GlobalSummary.SummaryMetrics.ViolationsBySeverity
	if sev["blocker"] == 0 {
		t.Errorf("expected blocker > 0, got violationsBySeverity=%v", sev)
	}
}

func TestExtractReport_Fail_HasFileReport(t *testing.T) {
	log := fixture(t, "fail.log")
	rpt, err := rulesvalidator.ExtractReport(log)
	if err != nil {
		t.Fatalf("ExtractReport(fail.log): unexpected error: %v", err)
	}
	if len(rpt.FileReport) == 0 {
		t.Fatal("expected at least one file report entry")
	}
}

// TestExtractReport_Error_ReturnsErrNoReport asserts the real behavior:
// when the JAR throws an exception (config error, missing -cf, etc.) it emits
// NO globalSummary JSON — only a Java exception stack trace.
// ExtractReport must return ErrNoReport, not a parsed report with status "ERROR".
// This matches what verify confirmed via two live JAR runs (InvalidConfigurationException,
// MismatchedInputException — both emitted zero JSON).
func TestExtractReport_Error_ReturnsErrNoReport(t *testing.T) {
	log := fixture(t, "error.log")
	_, err := rulesvalidator.ExtractReport(log)
	if !errors.Is(err, rulesvalidator.ErrNoReport) {
		t.Errorf("ExtractReport(error.log): expected ErrNoReport for real no-JSON JAR error output, got: %v", err)
	}
}

func TestExtractReport_NoisyPass_ExtractsCorrectly(t *testing.T) {
	log := fixture(t, "noisy_pass.log")
	rpt, err := rulesvalidator.ExtractReport(log)
	if err != nil {
		t.Fatalf("ExtractReport(noisy_pass.log): unexpected error: %v", err)
	}
	if rpt.GlobalSummary.Status != "PASS" {
		t.Errorf("status = %q, want PASS", rpt.GlobalSummary.Status)
	}
}

func TestExtractReport_NoJSON_ReturnsErrNoReport(t *testing.T) {
	_, err := rulesvalidator.ExtractReport("no json here at all")
	if !errors.Is(err, rulesvalidator.ErrNoReport) {
		t.Errorf("expected ErrNoReport, got %v", err)
	}
}

func TestExtractReport_MalformedJSON_ReturnsErrNoReport(t *testing.T) {
	// Embed a truncated/malformed JSON that contains globalSummary anchor.
	input := `[main] INFO CliServiceImpl - {"globalSummary": {"status": "PASS", BROKEN`
	_, err := rulesvalidator.ExtractReport(input)
	if !errors.Is(err, rulesvalidator.ErrNoReport) {
		t.Errorf("expected ErrNoReport for malformed JSON, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Decide
// ---------------------------------------------------------------------------

func TestDecide_Pass_ReturnsNil(t *testing.T) {
	log := fixture(t, "pass.log")
	rpt, _ := rulesvalidator.ExtractReport(log)
	if err := rulesvalidator.Decide(rpt); err != nil {
		t.Errorf("Decide(PASS): expected nil, got %v", err)
	}
}

func TestDecide_Fail_ReturnsError(t *testing.T) {
	log := fixture(t, "fail.log")
	rpt, _ := rulesvalidator.ExtractReport(log)
	if err := rulesvalidator.Decide(rpt); err == nil {
		t.Error("Decide(FAIL): expected non-nil error, got nil")
	}
}

// TestDecide_Error_ReturnsError asserts that Decide rejects a Report with status
// "ERROR". In practice the real JAR never emits ERROR JSON (it throws exceptions
// with no JSON output), but the gate must still treat ERROR as abort if such a
// report were ever produced (belt-and-suspenders fail-safe).
func TestDecide_Error_ReturnsError(t *testing.T) {
	rpt := rulesvalidator.Report{
		GlobalSummary: rulesvalidator.GlobalSummary{Status: "ERROR"},
	}
	if err := rulesvalidator.Decide(rpt); err == nil {
		t.Error("Decide(ERROR): expected non-nil error, got nil")
	}
}

func TestDecide_UnknownStatus_ReturnsError(t *testing.T) {
	rpt := rulesvalidator.Report{
		GlobalSummary: rulesvalidator.GlobalSummary{Status: "UNKNOWN"},
	}
	if err := rulesvalidator.Decide(rpt); err == nil {
		t.Error("Decide(UNKNOWN): expected non-nil error (fail-safe), got nil")
	}
}

func TestDecide_EmptyStatus_ReturnsError(t *testing.T) {
	rpt := rulesvalidator.Report{}
	if err := rulesvalidator.Decide(rpt); err == nil {
		t.Error("Decide(empty status): expected non-nil error (fail-safe), got nil")
	}
}

// Regression: exit code 0 with FAIL status must still produce an error.
// The gate is based SOLELY on the JSON status; exit code is never consulted.
func TestDecide_FailWithExitCode0_ReturnsError(t *testing.T) {
	log := fixture(t, "fail.log")
	rpt, err := rulesvalidator.ExtractReport(log)
	if err != nil {
		t.Fatalf("ExtractReport: %v", err)
	}
	// Simulate: container exited with code 0 but status=FAIL in JSON.
	// Decide() must ignore the exit code entirely.
	decideErr := rulesvalidator.Decide(rpt)
	if decideErr == nil {
		t.Error("Decide(FAIL, exit=0): expected error — exit code must NOT override JSON gate")
	}
}

// ---------------------------------------------------------------------------
// Error message content (formatViolations via Decide)
// ---------------------------------------------------------------------------

func TestDecide_Fail_ErrorContainsStatus(t *testing.T) {
	log := fixture(t, "fail.log")
	rpt, _ := rulesvalidator.ExtractReport(log)
	err := rulesvalidator.Decide(rpt)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "FAIL") {
		t.Errorf("error message missing status; got: %s", err.Error())
	}
}

func TestDecide_Fail_ErrorContainsSeverityCounts(t *testing.T) {
	log := fixture(t, "fail.log")
	rpt, _ := rulesvalidator.ExtractReport(log)
	err := rulesvalidator.Decide(rpt)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	// Should mention severity counts (e.g. "blocker:3").
	if !strings.Contains(msg, "blocker") {
		t.Errorf("error message missing blocker count; got: %s", msg)
	}
}

func TestDecide_Fail_ErrorContainsFileName(t *testing.T) {
	log := fixture(t, "fail.log")
	rpt, _ := rulesvalidator.ExtractReport(log)
	err := rulesvalidator.Decide(rpt)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "N0001_DDL_TBL_BAD.sql") {
		t.Errorf("error message missing offending file name; got: %s", err.Error())
	}
}
