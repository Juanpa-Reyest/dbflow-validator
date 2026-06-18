package report

import (
	"fmt"
	"strings"
	"time"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
)

// RenderExecLog produces the full structured execution.log document from the
// collected run report. The document layout (top to bottom):
//
//  1. Banner (with injected buildVersion)
//  2. RUN header: run-id, branch, schema (when known)
//  3. Step summary table: status glyph ✔/✘, step number, name, duration;
//     failing step shows error on an indented └─ line
//  4. RESULT line: ✔/✘ STATUS  total <dur>; on FAILED includes workspace pointer
//  5. DETALLE DE EJECUCIÓN: one block per step using style 3.b:
//     ┌─[ STEP NN ]── NAME ──… ✔/✘ <dur> ─┐
//     │  <trace lines>
//     └─────────────────────────────────────┘
//
// Parameters:
//   - rpt:          the completed RunReport from the orchestrator
//   - runID:        the run directory name (e.g. "2026-06-18_19-45-06")
//   - buildVersion: version string injected via ldflags (falls back to "dev")
//   - schema:       detected schema name (e.g. "scgolfcore"); empty = omit from header
func RenderExecLog(rpt domain.RunReport, runID, buildVersion, schema string) string {
	var sb strings.Builder

	// 1. Banner
	sb.WriteString(Banner(buildVersion))
	sb.WriteString("\n")

	// 2. RUN header
	writeRunHeader(&sb, rpt, runID, schema)
	sb.WriteString("\n")

	// 3. Step summary table
	writeStepSummaryTable(&sb, rpt.Steps)
	sb.WriteString("\n")

	// 4. RESULT line
	writeResultLine(&sb, rpt)
	sb.WriteString("\n")

	// 5. DETALLE DE EJECUCIÓN
	writeDetailBlocks(&sb, rpt.Steps)

	return sb.String()
}

// writeRunHeader writes: RUN <runID> · branch: <branch> · schema: <schema>
func writeRunHeader(sb *strings.Builder, rpt domain.RunReport, runID, schema string) {
	line := fmt.Sprintf("  RUN  %s", runID)
	if rpt.BaseBranch != "" {
		line += fmt.Sprintf("   ·   branch: %s", rpt.BaseBranch)
	}
	if schema != "" {
		line += fmt.Sprintf("   ·   schema: %s", schema)
	}
	sb.WriteString(line)
	sb.WriteString("\n")
}

// writeStepSummaryTable writes one line per step with status glyph, number, name, duration.
// A failing step additionally shows its error on an indented └─ line.
func writeStepSummaryTable(sb *strings.Builder, steps []domain.StepResult) {
	for i, s := range steps {
		glyph := stepGlyph(s.Status)
		dur := formatDuration(s.Duration)
		sb.WriteString(fmt.Sprintf("   %s %2d  %-25s  %s\n", glyph, i+1, s.Name, dur))
		if s.Status == domain.StepStatusFailed && s.Error != "" {
			sb.WriteString(fmt.Sprintf("          └─ ERROR: %s\n", s.Error))
		}
	}
}

// writeResultLine writes: RESULT  ✔/✘ <STATUS>   total <dur>
func writeResultLine(sb *strings.Builder, rpt domain.RunReport) {
	glyph := "✔"
	if rpt.Status != domain.StatusPassed {
		glyph = "✘"
	}
	totalDur := time.Duration(rpt.TotalDurMs) * time.Millisecond
	sb.WriteString(fmt.Sprintf("   RESULT   %s  %-12s  total %s\n",
		glyph, string(rpt.Status), formatDuration(totalDur)))
}

// writeDetailBlocks writes the DETALLE DE EJECUCIÓN section with one block per step.
// Block style 3.b from the design:
//
//	┌─[ STEP NN ]── NAME ──────────────────────── ✔/✘ <dur> ─┐
//	│  <trace line>
//	└──────────────────────────────────────────────────────────┘
func writeDetailBlocks(sb *strings.Builder, steps []domain.StepResult) {
	sb.WriteString("─────────────────────────────────────────────────────────────────\n")
	sb.WriteString("  DETALLE DE EJECUCIÓN\n")
	sb.WriteString("─────────────────────────────────────────────────────────────────\n\n")

	for i, s := range steps {
		glyph := stepGlyph(s.Status)
		dur := formatDuration(s.Duration)
		stepLabel := fmt.Sprintf("STEP %02d", i+1)
		name := strings.ToUpper(s.Name)

		// Build the top border: ┌─[ STEP NN ]── NAME ───…─── ✔/✘ dur ─┐
		// Total width: 65 chars
		const totalWidth = 65
		prefix := fmt.Sprintf("┌─[ %s ]── %s ", stepLabel, name)
		suffix := fmt.Sprintf(" %s %s ─┐", glyph, dur)
		fillLen := totalWidth - len([]rune(prefix)) - len([]rune(suffix))
		if fillLen < 1 {
			fillLen = 1
		}
		fill := strings.Repeat("─", fillLen)
		sb.WriteString(prefix + fill + suffix + "\n")

		// Trace lines with left rail.
		trace := strings.TrimRight(s.Trace, "\n")
		if trace != "" {
			for _, line := range strings.Split(trace, "\n") {
				sb.WriteString(fmt.Sprintf("│  %s\n", line))
			}
		} else {
			sb.WriteString("│  (no trace captured)\n")
		}
		// Error line in block when step failed.
		if s.Status == domain.StepStatusFailed && s.Error != "" {
			sb.WriteString(fmt.Sprintf("│  ERROR: %s\n", s.Error))
		}

		// Bottom border.
		bottom := strings.Repeat("─", totalWidth)
		sb.WriteString("└" + bottom + "┘\n\n")
	}
}

// stepGlyph returns ✔ for passed, ✘ for everything else.
func stepGlyph(status domain.StepStatus) string {
	if status == domain.StepStatusPassed {
		return "✔"
	}
	return "✘"
}

// formatDuration produces a human-readable duration string:
// < 1s → "NNms", >= 1s → "NNs", >= 60s → "Nm Ns".
func formatDuration(d time.Duration) string {
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
