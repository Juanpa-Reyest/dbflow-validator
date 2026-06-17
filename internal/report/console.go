package report

import (
	"fmt"
	"io"
	"os"
	"strings"

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
