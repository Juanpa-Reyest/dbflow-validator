package report_test

import (
	"strings"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/report"
)

func TestBanner_ContainsDBFLOWASCIIArt(t *testing.T) {
	got := report.Banner("dev")

	// The locked ASCII art must appear verbatim (spot-check key lines).
	artLines := []string{
		"██████╗ ██████╗ ███████╗██╗      ██████╗ ██╗    ██╗",
		"██╔══██╗██╔══██╗██╔════╝██║     ██╔═══██╗██║    ██║",
		"██║  ██║██████╔╝█████╗  ██║     ██║   ██║██║ █╗ ██║",
		"██║  ██║██╔══██╗██╔══╝  ██║     ██║   ██║██║███╗██║",
		"██████╔╝██████╔╝██║     ███████╗╚██████╔╝╚███╔███╔╝",
		"╚═════╝ ╚═════╝ ╚═╝     ╚══════╝ ╚═════╝  ╚══╝╚══╝",
	}
	for _, line := range artLines {
		if !strings.Contains(got, line) {
			t.Errorf("banner missing ASCII art line: %q", line)
		}
	}
}

func TestBanner_ContainsTagline(t *testing.T) {
	got := report.Banner("dev")
	if !strings.Contains(got, "V · A · L · I · D · A · T · O · R") {
		t.Error("banner missing VALIDATOR spaced tagline")
	}
	if !strings.Contains(got, "Local database-change validation · fail fast before the PR") {
		t.Error("banner missing tagline line 1")
	}
	if !strings.Contains(got, "zero side-effects") {
		t.Error("banner missing tagline line 2")
	}
}

func TestBanner_ContainsSignature(t *testing.T) {
	got := report.Banner("dev")
	if !strings.Contains(got, "✒  Juanpa Reyest · Development Engineer") {
		t.Error("banner missing signature")
	}
}

func TestBanner_ContainsTerminalIcon(t *testing.T) {
	got := report.Banner("dev")
	if !strings.Contains(got, "╭───────────╮") {
		t.Error("banner missing terminal icon top")
	}
	if !strings.Contains(got, "│ ▸ ~/ _     │") {
		t.Error("banner missing terminal icon middle")
	}
	if !strings.Contains(got, "╰───────────╯") {
		t.Error("banner missing terminal icon bottom")
	}
}

func TestBanner_InjectsVersion(t *testing.T) {
	// The buildVersion must replace the static "v0.1" placeholder.
	got := report.Banner("v1.2.3")
	if !strings.Contains(got, "v1.2.3") {
		t.Errorf("banner does not contain injected version v1.2.3; got:\n%s", got)
	}
}

func TestBanner_FallbackVersion(t *testing.T) {
	// Empty string should fall back to "dev".
	got := report.Banner("")
	if !strings.Contains(got, "dev") {
		t.Errorf("banner with empty version should contain 'dev'; got:\n%s", got)
	}
}

func TestBanner_DevVersionDefault(t *testing.T) {
	got := report.Banner("dev")
	if !strings.Contains(got, "dev") {
		t.Errorf("banner should contain 'dev' version; got:\n%s", got)
	}
}

func TestBanner_VersionNotHardcoded(t *testing.T) {
	// "v0.1" must NOT appear literally — it should be replaced by injected version.
	got := report.Banner("v2.0.0")
	if strings.Contains(got, "v0.1") {
		t.Error("banner contains hardcoded 'v0.1' — version must be injected")
	}
}

func TestBanner_HasBorderLines(t *testing.T) {
	got := report.Banner("dev")
	// Top/bottom double-line borders from the design.
	if !strings.Contains(got, "══════════════════════════════════════════════════════════════════") {
		t.Error("banner missing top/bottom double-line border")
	}
	if !strings.Contains(got, "──────────────────────────────────────────────────────────────────") {
		t.Error("banner missing middle single-line separator")
	}
}
