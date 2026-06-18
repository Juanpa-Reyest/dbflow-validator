package main

// Tests for the console output ordering contract:
// Banner MUST appear before any progress line (▸), and progress lines MUST
// appear before the summary table (✔/✘ lines from RenderQuiet).
//
// The wiring lives in main.go: banner is printed once before orchestrator.Run,
// progress is emitted live via OnStep during the run, and RenderQuiet is called
// at the end. These tests exercise the helper functions in isolation to lock
// that contract.

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
	"github.com/dbflow-validator/dbflow-validator/internal/orchestrator"
	"github.com/dbflow-validator/dbflow-validator/internal/report"
)

// simulatedPassedReport returns a minimal RunReport for ordering tests.
func simulatedPassedReport() domain.RunReport {
	return domain.RunReport{
		Status:     domain.StatusPassed,
		RepoURL:    "https://example.com/repo.git",
		BaseBranch: "integration",
		Steps: []domain.StepResult{
			{Name: "preflight", Status: domain.StepStatusPassed, DurationMs: 15,
				Duration: 15 * time.Millisecond},
			{Name: "clone", Status: domain.StepStatusPassed, DurationMs: 500,
				Duration: 500 * time.Millisecond},
		},
		TotalDurMs: 515,
		Started:    time.Now(),
		Ended:      time.Now().Add(515 * time.Millisecond),
	}
}

// TestConsoleOrder_BannerBeforeProgressLines verifies that the banner text
// appears in the combined console output BEFORE any progress line (▸ marker).
// This locks the contract: banner first, then live step progress.
func TestConsoleOrder_BannerBeforeProgressLines(t *testing.T) {
	var buf bytes.Buffer

	// 1. Banner is printed FIRST (desired behavior after the fix).
	buf.WriteString(report.Banner("test-v"))

	// 2. Progress lines are emitted live during orchestrator.Run.
	printer := consoleProgressPrinter(&buf)
	printer(orchestrator.StepEvent{Name: "preflight", Done: false})
	printer(orchestrator.StepEvent{Name: "preflight", Done: true, Failed: false})
	printer(orchestrator.StepEvent{Name: "clone", Done: false})
	printer(orchestrator.StepEvent{Name: "clone", Done: true, Failed: false})

	// 3. RenderQuiet (summary table + RESULT) is printed LAST.
	report.NewConsoleRenderer().RenderQuiet(simulatedPassedReport(), "", &buf)

	out := buf.String()

	// Banner must contain the subtitle line unique to the banner template.
	// "V · A · L · I · D · A · T · O · R" appears only in the banner.
	bannerMarker := "V · A · L"
	bannerIdx := strings.Index(out, bannerMarker)
	if bannerIdx < 0 {
		t.Fatal("console output does not contain banner (expected 'V · A · L' from banner subtitle)")
	}

	// First progress line must appear after the banner.
	firstProgressIdx := strings.Index(out, "▸")
	if firstProgressIdx < 0 {
		t.Fatal("console output does not contain any progress line (expected '▸')")
	}
	if bannerIdx >= firstProgressIdx {
		t.Errorf("banner appears at index %d but first progress line is at index %d; "+
			"banner must come BEFORE progress lines", bannerIdx, firstProgressIdx)
	}

	// Summary table (✔/✘) must appear after progress lines.
	firstTableIdx := strings.Index(out, "✔")
	if firstTableIdx < 0 {
		t.Fatal("console output does not contain summary table glyph (expected '✔')")
	}
	if firstProgressIdx >= firstTableIdx {
		t.Errorf("first progress line (▸) is at index %d but first table glyph (✔) is at "+
			"index %d; progress must come BEFORE the summary table", firstProgressIdx, firstTableIdx)
	}
}

// TestConsoleOrder_BannerAppearsOnce verifies that RenderQuiet does NOT include
// a second banner. The banner must appear exactly once in the combined output.
func TestConsoleOrder_BannerAppearsOnce(t *testing.T) {
	var buf bytes.Buffer

	// Banner printed once at the start.
	buf.WriteString(report.Banner("v1.2.3"))

	// RenderQuiet must NOT re-emit the banner.
	report.NewConsoleRenderer().RenderQuiet(simulatedPassedReport(), "", &buf)

	out := buf.String()

	// Count banner occurrences by counting the unique banner separator line.
	// The banner ends with: ══════════════════════...
	bannerSepCount := strings.Count(out, "══════════════════════════════════════════════════════════════════")
	// There are two separator lines in one banner (top and bottom). So exactly two
	// occurrences means exactly one banner.
	if bannerSepCount != 2 {
		t.Errorf("expected banner separator to appear exactly 2 times (one banner = top+bottom), "+
			"got %d — banner may be printed more than once or not at all", bannerSepCount)
	}
}

// TestConsoleOrder_RenderQuietNoBanner verifies that RenderQuiet alone does NOT
// emit the banner. The caller (main.go) is responsible for printing the banner first.
func TestConsoleOrder_RenderQuietNoBanner(t *testing.T) {
	var buf bytes.Buffer
	report.NewConsoleRenderer().RenderQuiet(simulatedPassedReport(), "", &buf)
	out := buf.String()

	if strings.Contains(out, "DBFLOW") {
		t.Error("RenderQuiet must NOT include the banner — main.go prints it once at the top")
	}
}
