package report

import (
	"encoding/json"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
)

// jsonStep is the JSON schema for a single step in the report.
type jsonStep struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	DurationMs int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
	Trace      string `json:"trace,omitempty"`
}

// jsonReport is the top-level JSON schema matching the spec.
type jsonReport struct {
	Status      string     `json:"status"`
	Timestamp   string     `json:"timestamp"`
	RepoURL     string     `json:"repo_url"`
	BaseBranch  string     `json:"base_branch"`
	Steps       []jsonStep `json:"steps"`
	TotalDurMs  int64      `json:"total_duration_ms"`
}

// JSONRenderer serializes a RunReport to JSON conforming to the spec schema.
type JSONRenderer struct{}

// NewJSONRenderer returns a JSONRenderer.
func NewJSONRenderer() *JSONRenderer { return &JSONRenderer{} }

// Render serializes report to indented JSON bytes.
// Credentials and tokens MUST NOT appear in the report — callers must ensure
// they are never placed in RunReport fields (the renderer does not filter them).
func (r *JSONRenderer) Render(report domain.RunReport) ([]byte, error) {
	steps := make([]jsonStep, len(report.Steps))
	for i, s := range report.Steps {
		steps[i] = jsonStep{
			Name:       s.Name,
			Status:     string(s.Status),
			DurationMs: s.DurationMs,
			Error:      s.Error,
			Trace:      s.Trace,
		}
	}

	out := jsonReport{
		Status:     string(report.Status),
		Timestamp:  report.Timestamp.UTC().Format("2006-01-02T15:04:05Z"),
		RepoURL:    report.RepoURL,
		BaseBranch: report.BaseBranch,
		Steps:      steps,
		TotalDurMs: report.TotalDurMs,
	}

	return json.MarshalIndent(out, "", "  ")
}
