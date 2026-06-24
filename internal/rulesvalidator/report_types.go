// Package rulesvalidator implements the PreSyncValidator seam using the
// library-script-validator JAR running in a containerised JVM.
//
// The validator reads a YAML ruleset and validates SQL files in the cloneRoot's
// SQLInput directory.  The JSON report written by the JAR to stderr (via slf4j)
// is parsed and gated: only globalSummary.status == "PASS" allows the pipeline
// to continue.
package rulesvalidator

// Report is the top-level JSON structure emitted by the validator JAR.
// Only the fields required for gate decisions are decoded; all others are ignored.
type Report struct {
	GlobalSummary GlobalSummary `json:"globalSummary"`
	FileReport    []FileReport  `json:"fileReport"`
}

// GlobalSummary carries the aggregate outcome of the validation run.
type GlobalSummary struct {
	Status         string          `json:"status"`
	Score          float64         `json:"score"`
	SummaryMetrics SummaryMetrics  `json:"summaryMetrics"`
}

// SummaryMetrics holds per-severity violation counts used in error messages.
type SummaryMetrics struct {
	ViolationsBySeverity map[string]int `json:"violationsBySeverity"`
}

// FileReport holds the per-file validation outcome.
type FileReport struct {
	FileName   string      `json:"fileName"`
	Status     string      `json:"status"`
	Score      float64     `json:"score"`
	Violations []Violation `json:"violations"`
}

// Violation is a single rule violation in a file.
type Violation struct {
	RuleID   string `json:"ruleId"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Line     int    `json:"line"`
}
