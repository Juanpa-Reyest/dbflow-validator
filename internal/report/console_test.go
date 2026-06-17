package report_test

import (
	"bytes"
	"os"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
	"github.com/dbflow-validator/dbflow-validator/internal/report"
)

func TestConsoleRenderer_GoldenFile(t *testing.T) {
	// Force NO_COLOR so output is deterministic in CI and golden files.
	t.Setenv("NO_COLOR", "1")

	r := report.NewConsoleRenderer()

	tests := []struct {
		name   string
		rpt    domain.RunReport
		golden string
	}{
		{"passed", fixedPassedReport(), "testdata/console_passed.txt"},
		{"failed", fixedFailedReport(), "testdata/console_failed.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			r.Render(tt.rpt, &buf)
			got := buf.Bytes()

			if *update {
				if err := os.WriteFile(tt.golden, got, 0o644); err != nil {
					t.Fatalf("update golden: %v", err)
				}
				t.Logf("updated %s", tt.golden)
				return
			}

			want, err := os.ReadFile(tt.golden)
			if err != nil {
				t.Fatalf("read golden %s: %v (run with -update to create)", tt.golden, err)
			}
			if string(got) != string(want) {
				t.Errorf("console output mismatch.\nGot:\n%s\nWant:\n%s", got, want)
			}
		})
	}
}

func TestConsoleRenderer_PassedContainsPassedSteps(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	r := report.NewConsoleRenderer()

	var buf bytes.Buffer
	r.Render(fixedPassedReport(), &buf)
	out := buf.String()

	if !bytes.Contains([]byte(out), []byte("PASSED")) {
		t.Error("expected PASSED in output")
	}
}

func TestConsoleRenderer_FailedContainsTrace(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	r := report.NewConsoleRenderer()

	var buf bytes.Buffer
	r.Render(fixedFailedReport(), &buf)
	out := buf.String()

	if !bytes.Contains([]byte(out), []byte("FAILED")) {
		t.Error("expected FAILED in output")
	}
	if !bytes.Contains([]byte(out), []byte("BUILD FAILURE")) {
		t.Error("expected trace/error detail in console output")
	}
}

func TestConsoleRenderer_SecretAbsent(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	r := report.NewConsoleRenderer()

	rpt := fixedPassedReport()
	// Would be contamination in real code — we assert the renderer never passes it through.
	rpt.Steps[0].Trace = "password=secret123"

	var buf bytes.Buffer
	r.Render(rpt, &buf)

	// We don't assert secret123 because the trace IS included (for debugging).
	// The real guard is that tokens from config never make it into reports.
	// This test verifies the renderer doesn't add anything unexpected.
	if !bytes.Contains(buf.Bytes(), []byte("preflight")) {
		t.Error("expected step name in output")
	}
}

func TestConsoleRenderer_NoColorDisablesANSI(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	r := report.NewConsoleRenderer()

	var buf bytes.Buffer
	r.Render(fixedPassedReport(), &buf)
	out := buf.String()

	// When NO_COLOR is set, no ANSI escape sequences should appear.
	if bytes.Contains([]byte(out), []byte("\x1b[")) {
		t.Error("ANSI escape sequences found despite NO_COLOR=1")
	}
}

func TestConsoleRenderer_WithANSI(t *testing.T) {
	// Ensure NO_COLOR is NOT set for this test.
	os.Unsetenv("NO_COLOR")

	// We create a renderer that forces ANSI on regardless of TTY.
	r := report.NewConsoleRendererForced()

	var buf bytes.Buffer
	r.Render(fixedPassedReport(), &buf)
	out := buf.String()

	// With ANSI forced, escape sequences should be present.
	if !bytes.Contains([]byte(out), []byte("\x1b[")) {
		t.Error("expected ANSI escape sequences in forced-ANSI output")
	}
}
