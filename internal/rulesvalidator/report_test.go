package rulesvalidator_test

import (
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
// Decide
// ---------------------------------------------------------------------------

func TestDecide_Pass_ReturnsNil(t *testing.T) {
	rpt := rulesvalidator.Report{
		GlobalSummary: rulesvalidator.GlobalSummary{Status: "PASS", Score: 100.0},
	}
	if err := rulesvalidator.Decide(rpt); err != nil {
		t.Errorf("Decide(PASS): expected nil, got %v", err)
	}
}

func TestDecide_Fail_ReturnsError(t *testing.T) {
	rpt := rulesvalidator.Report{
		GlobalSummary: rulesvalidator.GlobalSummary{Status: "FAIL", Score: 5.9},
	}
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
	rpt := rulesvalidator.Report{
		GlobalSummary: rulesvalidator.GlobalSummary{Status: "FAIL", Score: 5.9},
	}
	// Simulate: container exited with code 0 but status=FAIL in JSON.
	// Decide() must ignore the exit code entirely.
	decideErr := rulesvalidator.Decide(rpt)
	if decideErr == nil {
		t.Error("Decide(FAIL, exit=0): expected error — exit code must NOT override JSON gate")
	}
}

// ---------------------------------------------------------------------------
// Error message content (formatViolations via Decide) — using JSON fixtures
// ---------------------------------------------------------------------------

func TestDecide_Fail_ErrorContainsStatus(t *testing.T) {
	rpt, err := rulesvalidator.ReadReportFile("testdata/fail_report.json")
	if err != nil {
		t.Fatalf("ReadReportFile: %v", err)
	}
	decideErr := rulesvalidator.Decide(rpt)
	if decideErr == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(decideErr.Error(), "FAIL") {
		t.Errorf("error message missing status; got: %s", decideErr.Error())
	}
}

func TestDecide_Fail_ErrorContainsSeverityCounts(t *testing.T) {
	rpt, err := rulesvalidator.ReadReportFile("testdata/fail_report.json")
	if err != nil {
		t.Fatalf("ReadReportFile: %v", err)
	}
	decideErr := rulesvalidator.Decide(rpt)
	if decideErr == nil {
		t.Fatal("expected error")
	}
	msg := decideErr.Error()
	if !strings.Contains(msg, "blocker") {
		t.Errorf("error message missing blocker count; got: %s", msg)
	}
}

func TestDecide_Fail_ErrorContainsFileName(t *testing.T) {
	rpt, err := rulesvalidator.ReadReportFile("testdata/fail_report.json")
	if err != nil {
		t.Fatalf("ReadReportFile: %v", err)
	}
	decideErr := rulesvalidator.Decide(rpt)
	if decideErr == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(decideErr.Error(), "N0001_DDL_TBL_BAD.sql") {
		t.Errorf("error message missing offending file name; got: %s", decideErr.Error())
	}
}
