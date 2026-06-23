package rulesvalidator

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrNoReport is returned by ExtractReport when the combined container log
// does not contain a parseable JSON report from the validator JAR.
var ErrNoReport = errors.New("rulesvalidator: no valid JSON report found in container output")

// ExtractReport locates and parses the JSON report embedded in the combined
// container output (stdout+stderr).
//
// The JAR emits the JSON via slf4j at INFO level with the prefix:
//
//	[main] INFO com.gs.ftt.coe_ds.script_validator_postgre.service.impl.CliServiceImpl - {
//
// The approach:
//  1. Find the "globalSummary" substring.
//  2. Walk backwards from that position to find the opening `{` that starts
//     the top-level object (past the slf4j ` - ` separator).
//  3. Walk forward counting brace depth (respecting string literals and
//     escape sequences) until depth reaches zero — this gives the balanced span.
//  4. Unmarshal the span into Report.
//
// Any failure (anchor not found, unbalanced braces, unmarshal error) returns
// ErrNoReport so callers can treat it as a hard abort (cannot prove PASS).
func ExtractReport(log string) (Report, error) {
	// Step 1: find the anchor.
	anchorIdx := strings.Index(log, `"globalSummary"`)
	if anchorIdx < 0 {
		return Report{}, fmt.Errorf("%w: anchor \"globalSummary\" not found", ErrNoReport)
	}

	// Step 2: walk backwards from anchorIdx to find the top-level '{'.
	// The slf4j line ends with " - {" before the JSON, so we look for
	// the first '{' that precedes the anchor.
	openBrace := -1
	for i := anchorIdx - 1; i >= 0; i-- {
		if log[i] == '{' {
			openBrace = i
			break
		}
	}
	if openBrace < 0 {
		return Report{}, fmt.Errorf("%w: opening brace not found before anchor", ErrNoReport)
	}

	// Step 3: walk forward from openBrace counting brace depth.
	jsonSpan := extractBalancedObject(log[openBrace:])
	if jsonSpan == "" {
		return Report{}, fmt.Errorf("%w: unbalanced braces in JSON span", ErrNoReport)
	}

	// Step 4: unmarshal.
	var rpt Report
	if err := json.Unmarshal([]byte(jsonSpan), &rpt); err != nil {
		return Report{}, fmt.Errorf("%w: json unmarshal: %v", ErrNoReport, err)
	}

	return rpt, nil
}

// extractBalancedObject returns the shortest prefix of s that forms a balanced
// JSON object (depth-zero after the opening '{').  Returns "" on failure.
// Respects string literals (including escape sequences) so braces inside
// strings are not counted.
func extractBalancedObject(s string) string {
	if len(s) == 0 || s[0] != '{' {
		return ""
	}

	depth := 0
	inString := false
	escaped := false

	for i, ch := range s {
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && inString {
			escaped = true
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch ch {
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				return s[:i+1]
			}
		}
	}

	return ""
}

// Decide applies the gate rule to a parsed Report.
//
// Gate:
//   - "PASS" → nil (pipeline continues)
//   - "FAIL", "ERROR", any other value, or empty → non-nil error (abort)
//
// The JAR exit code is NEVER consulted.  Returning nil ONLY when status is
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
