package rulesvalidator

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrNoReport is returned when the validator JSON report is absent or unparseable.
// Callers must treat this as a hard abort (fail-closed: cannot prove PASS).
var ErrNoReport = errors.New("rulesvalidator: no valid JSON report found")

// outputReportRelPath is the path of the JSON report relative to the -output dir
// that the JAR writes when invoked with the -output flag.
const outputReportRelPath = "report/json/validation_report.json"

// outputDirRelPath is the path of the -output directory relative to cloneRoot.
const outputDirRelPath = "src/main/resources/Validator/outputReport"

// ReportPath returns the absolute host-side path where the JAR writes the JSON
// report when invoked with "-output <cloneRoot>/outputDirRelPath".
func ReportPath(cloneRoot string) string {
	return filepath.Join(cloneRoot, filepath.FromSlash(outputDirRelPath), filepath.FromSlash(outputReportRelPath))
}

// ReadReportFile reads and parses the clean JSON report file that the validator JAR
// writes to disk when invoked with the -output flag.
//
// The file at path must contain pure JSON (no log prefixes, no ANTLR noise).
// Any failure — file absent, unreadable, or invalid JSON — returns ErrNoReport
// so callers fail-closed (cannot prove PASS without a parseable report).
func ReadReportFile(path string) (Report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Report{}, fmt.Errorf("%w: read report file %s: %v", ErrNoReport, path, err)
	}
	var rpt Report
	if err := json.Unmarshal(data, &rpt); err != nil {
		return Report{}, fmt.Errorf("%w: json unmarshal %s: %v", ErrNoReport, path, err)
	}
	return rpt, nil
}

// Decide applies the gate rule to a parsed Report.
//
// Gate:
//   - "PASS" → nil (pipeline continues)
//   - "FAIL", "ERROR", any other value, or empty → non-nil error (abort)
//
// The JAR exit code is NEVER consulted. Returning nil ONLY when status is
// explicitly "PASS" ensures that any novel status value defaults to abort
// (fail-safe).
//
// The error message produced for non-PASS outcomes is actionable: it includes
// the status, the violationsBySeverity counts, and the names of offending files.
func Decide(rpt Report) error {
	if rpt.GlobalSummary.Status == "PASS" {
		return nil
	}
	return errors.New(formatViolations(rpt))
}

// formatViolations builds a human-readable summary of a non-PASS report.
// It includes: status, score, severity counts, and offending file names.
func formatViolations(rpt Report) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(
		"SQL rules validation failed: status=%s score=%.1f",
		rpt.GlobalSummary.Status,
		rpt.GlobalSummary.Score,
	))

	// Severity counts.
	sev := rpt.GlobalSummary.SummaryMetrics.ViolationsBySeverity
	if len(sev) > 0 {
		sb.WriteString(" violations=[")
		// Sort keys for deterministic output.
		keys := make([]string, 0, len(sev))
		for k := range sev {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i, k := range keys {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(fmt.Sprintf("%s:%d", k, sev[k]))
		}
		sb.WriteString("]")
	}

	// Offending files (those with non-PASS status and at least one violation).
	const maxFiles = 5
	count := 0
	for _, fr := range rpt.FileReport {
		if fr.Status == "FAIL" || fr.Status == "ERROR" {
			if count == 0 {
				sb.WriteString(" files=[")
			} else {
				sb.WriteString(", ")
			}
			sb.WriteString(fr.FileName)
			count++
			if count >= maxFiles {
				break
			}
		}
	}
	if count > 0 {
		sb.WriteString("]")
	}

	return sb.String()
}
