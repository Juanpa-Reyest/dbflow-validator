package report_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
	"github.com/dbflow-validator/dbflow-validator/internal/report"
)

var quietFixedTime = time.Date(2026, 6, 18, 19, 45, 6, 0, time.UTC)

func quietPassedReport() domain.RunReport {
	return domain.RunReport{
		Status:     domain.StatusPassed,
		Timestamp:  quietFixedTime,
		RepoURL:    "https://example.com/repo.git",
		BaseBranch: "integration",
		Steps: []domain.StepResult{
			{Name: "preflight", Status: domain.StepStatusPassed, DurationMs: 16,
				Duration: 16 * time.Millisecond},
			{Name: "dbflow:sync", Status: domain.StepStatusPassed, DurationMs: 26000,
				Duration: 26 * time.Second, Trace: strings.Repeat("[INFO] ...\n", 100)},
			{Name: "dbflow:rollback", Status: domain.StepStatusPassed, DurationMs: 13000,
				Duration: 13 * time.Second, Trace: strings.Repeat("[INFO] ...\n", 80)},
		},
		TotalDurMs: 39016,
		Started:    quietFixedTime,
		Ended:      quietFixedTime.Add(39016 * time.Millisecond),
	}
}

func quietFailedReport() domain.RunReport {
	return domain.RunReport{
		Status:     domain.StatusFailed,
		Timestamp:  quietFixedTime,
		RepoURL:    "https://example.com/repo.git",
		BaseBranch: "integration",
		Steps: []domain.StepResult{
			{Name: "preflight", Status: domain.StepStatusPassed, DurationMs: 16,
				Duration: 16 * time.Millisecond},
			{Name: "dbflow:rollback", Status: domain.StepStatusFailed, DurationMs: 13000,
				Duration: 13 * time.Second,
				Error:    `duplicate key violates "PK_TC_ESTATUS"`,
				Trace:    strings.Repeat("[ERROR] ...\n", 200)},
		},
		TotalDurMs: 13016,
		Started:    quietFixedTime,
		Ended:      quietFixedTime.Add(13016 * time.Millisecond),
	}
}

// TestRenderQuiet_HasResultLine verifies the RESULT line appears.
func TestRenderQuiet_HasResultLine(t *testing.T) {
	r := report.NewConsoleRenderer()
	var buf bytes.Buffer
	r.RenderQuiet(quietPassedReport(), "", &buf)
	out := buf.String()

	if !strings.Contains(out, "RESULT") {
		t.Error("quiet console missing RESULT line")
	}
	if !strings.Contains(out, "PASSED") {
		t.Error("quiet console missing PASSED in RESULT line")
	}
}

// TestRenderQuiet_HasStepProgressLines verifies one concise line per step.
func TestRenderQuiet_HasStepProgressLines(t *testing.T) {
	r := report.NewConsoleRenderer()
	var buf bytes.Buffer
	r.RenderQuiet(quietPassedReport(), "", &buf)
	out := buf.String()

	for _, name := range []string{"preflight", "dbflow:sync", "dbflow:rollback"} {
		if !strings.Contains(out, name) {
			t.Errorf("quiet console missing step progress line for %q", name)
		}
	}
}

// TestRenderQuiet_DoesNotContainVerboseTrace verifies long Maven traces are absent.
func TestRenderQuiet_DoesNotContainVerboseTrace(t *testing.T) {
	r := report.NewConsoleRenderer()
	var buf bytes.Buffer
	// The step trace has 100 lines of [INFO] ...; console should NOT dump them all.
	r.RenderQuiet(quietPassedReport(), "", &buf)
	out := buf.String()

	// Count [INFO] occurrences — quiet mode should not dump raw Maven output.
	count := strings.Count(out, "[INFO] ...")
	if count > 5 {
		t.Errorf("quiet console emitted %d [INFO] trace lines; expected at most 5", count)
	}
}

// TestRenderQuiet_PointerToExecLog verifies the closing pointer to execution.log.
func TestRenderQuiet_PointerToExecLog(t *testing.T) {
	r := report.NewConsoleRenderer()
	var buf bytes.Buffer
	r.RenderQuiet(quietPassedReport(), "/runs/2026-06-18_19-45-06", &buf)
	out := buf.String()

	if !strings.Contains(out, "execution.log") {
		t.Error("quiet console missing pointer to execution.log")
	}
}

// TestRenderQuiet_FailedShowsGlyph verifies ✘ glyph on failed steps.
func TestRenderQuiet_FailedShowsGlyph(t *testing.T) {
	r := report.NewConsoleRenderer()
	var buf bytes.Buffer
	r.RenderQuiet(quietFailedReport(), "", &buf)
	out := buf.String()

	if !strings.Contains(out, "✘") {
		t.Error("quiet console missing ✘ glyph for failed run")
	}
}

// TestRenderQuiet_PassedShowsGlyph verifies ✔ glyph on passed steps.
func TestRenderQuiet_PassedShowsGlyph(t *testing.T) {
	r := report.NewConsoleRenderer()
	var buf bytes.Buffer
	r.RenderQuiet(quietPassedReport(), "", &buf)
	out := buf.String()

	if !strings.Contains(out, "✔") {
		t.Error("quiet console missing ✔ glyph for passed step")
	}
}
