package report

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
)

// ANSI color codes.
const (
	ansiGreen  = "\x1b[32m"
	ansiRed    = "\x1b[31m"
	ansiYellow = "\x1b[33m"
	ansiBold   = "\x1b[1m"
	ansiReset  = "\x1b[0m"
)

// ConsoleRenderer writes a human-readable ANSI-colored validation report.
// ANSI codes are suppressed automatically when NO_COLOR is set or the output
// is not a TTY. Use NewConsoleRendererForced to bypass the TTY check (e.g. in tests).
type ConsoleRenderer struct {
	forceColor bool
}

// NewConsoleRenderer returns a ConsoleRenderer that auto-detects color support.
func NewConsoleRenderer() *ConsoleRenderer { return &ConsoleRenderer{} }

// NewConsoleRendererForced returns a ConsoleRenderer that always emits ANSI codes.
// Use in tests where color output needs to be verified regardless of TTY.
func NewConsoleRendererForced() *ConsoleRenderer { return &ConsoleRenderer{forceColor: true} }

// Render writes a formatted validation report to w.
func (r *ConsoleRenderer) Render(report domain.RunReport, w io.Writer) {
	useColor := r.forceColor || (os.Getenv("NO_COLOR") == "" && isTerminal(w))

	colorize := func(code, s string) string {
		if !useColor {
			return s
		}
		return code + s + ansiReset
	}

	// Overall status banner.
	var statusColor string
	switch report.Status {
	case domain.StatusPassed:
		statusColor = ansiGreen
	case domain.StatusFailed:
		statusColor = ansiRed
	default:
		statusColor = ansiYellow
	}

	banner := colorize(ansiBold+statusColor, fmt.Sprintf("=== %s ===", report.Status))
	fmt.Fprintf(w, "\n%s\n", banner)
	fmt.Fprintf(w, "Repo:       %s\n", report.RepoURL)
	fmt.Fprintf(w, "Branch:     %s\n", report.BaseBranch)
	fmt.Fprintf(w, "Duration:   %d ms\n", report.TotalDurMs)
	fmt.Fprintf(w, "\nSteps:\n")

	for _, step := range report.Steps {
		var stepColor string
		switch step.Status {
		case domain.StepStatusPassed:
			stepColor = ansiGreen
		case domain.StepStatusFailed:
			stepColor = ansiRed
		case domain.StepStatusSkipped:
			stepColor = ansiYellow
		default:
			stepColor = ansiYellow
		}

		status := colorize(stepColor, string(step.Status))
		fmt.Fprintf(w, "  [%s] %-30s %d ms\n", status, step.Name, step.DurationMs)

		if step.Error != "" {
			fmt.Fprintf(w, "         Error: %s\n", step.Error)
		}
		if step.Trace != "" {
			fmt.Fprintf(w, "         Trace:\n")
			for _, line := range strings.Split(step.Trace, "\n") {
				fmt.Fprintf(w, "           %s\n", line)
			}
		}
	}
	fmt.Fprintf(w, "\n")
}

// RenderQuiet writes a concise, human-readable summary to w suitable for the terminal.
// It shows only:
//   - one high-level progress line per step (▸ <name> … OK/FAILED <dur>)
//   - the overall RESULT line
//   - a closing pointer to execution.log (when runDir is non-empty)
//
// Verbose Maven traces and minute-level detail are omitted; they live in execution.log.
func (r *ConsoleRenderer) RenderQuiet(rpt domain.RunReport, runDir string, w io.Writer) {
	totalDur := time.Duration(rpt.TotalDurMs) * time.Millisecond

	for _, s := range rpt.Steps {
		glyph := "✔"
		label := "OK"
		if s.Status != domain.StepStatusPassed {
			glyph = "✘"
			label = string(s.Status)
		}
		dur := formatConsoleDuration(s.Duration)
		fmt.Fprintf(w, "  %s %-30s %s (%s)\n", glyph, s.Name, label, dur)
	}

	fmt.Fprintln(w)

	overallGlyph := "✔"
	if rpt.Status != domain.StatusPassed {
		overallGlyph = "✘"
	}
	fmt.Fprintf(w, "  RESULT  %s  %-12s  total %s\n",
		overallGlyph, string(rpt.Status), formatConsoleDuration(totalDur))

	if runDir != "" {
		logPath := filepath.Join(runDir, "execution.log")
		fmt.Fprintf(w, "\n  Detalles completos → %s\n", logPath)
	}
}

// formatConsoleDuration formats duration for console display.
func formatConsoleDuration(d time.Duration) string {
	if d == 0 {
		return "0ms"
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	if s == 0 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dm %ds", m, s)
}

// isTerminal returns true if w is likely a file descriptor attached to a terminal.
// This is a best-effort check; it always returns false for non-*os.File writers.
func isTerminal(w io.Writer) bool {
	if f, ok := w.(*os.File); ok {
		fi, err := f.Stat()
		if err != nil {
			return false
		}
		return (fi.Mode() & os.ModeCharDevice) != 0
	}
	return false
}
