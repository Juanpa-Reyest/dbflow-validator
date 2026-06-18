package domain

import "time"

// Status is the overall outcome of a validation run.
type Status string

const (
	StatusPassed     Status = "PASSED"
	StatusFailed     Status = "FAILED"
	StatusAborted    Status = "ABORTED"
	// StatusUsageError indicates a configuration or usage problem (missing SQLInput,
	// bad flags, etc.). main.go maps this to exit code 2.
	StatusUsageError Status = "USAGE_ERROR"
)

// StepStatus is the outcome of a single validation step.
type StepStatus string

const (
	StepStatusPassed  StepStatus = "PASSED"
	StepStatusFailed  StepStatus = "FAILED"
	StepStatusSkipped StepStatus = "SKIPPED"
	StepStatusAborted StepStatus = "ABORTED"
)

// StepResult holds the outcome, timing, and trace for one validation step.
type StepResult struct {
	Name       string        `json:"name"`
	Status     StepStatus    `json:"status"`
	Duration   time.Duration `json:"-"`
	DurationMs int64         `json:"duration_ms"`
	Error      string        `json:"error,omitempty"`
	Trace      string        `json:"trace,omitempty"`
}

// RunReport is the top-level result emitted after a full validation run.
type RunReport struct {
	Status      Status       `json:"status"`
	Timestamp   time.Time    `json:"timestamp"`
	RepoURL     string       `json:"repo_url"`
	BaseBranch  string       `json:"base_branch"`
	Steps       []StepResult `json:"steps"`
	TotalDurMs  int64        `json:"total_duration_ms"`
	Started     time.Time    `json:"-"`
	Ended       time.Time    `json:"-"`
}
