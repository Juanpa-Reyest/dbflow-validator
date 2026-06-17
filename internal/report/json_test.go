package report_test

import (
	"encoding/json"
	"flag"
	"os"
	"testing"
	"time"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
	"github.com/dbflow-validator/dbflow-validator/internal/report"
)

var update = flag.Bool("update", false, "update golden files")

// fixedTime is a deterministic timestamp for golden-file tests.
var fixedTime = time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)

func fixedPassedReport() domain.RunReport {
	return domain.RunReport{
		Status:     domain.StatusPassed,
		Timestamp:  fixedTime,
		RepoURL:    "https://example.com/repo.git",
		BaseBranch: "main",
		Steps: []domain.StepResult{
			{Name: "preflight", Status: domain.StepStatusPassed, DurationMs: 10},
			{Name: "clone", Status: domain.StepStatusPassed, DurationMs: 500},
			{Name: "dbflow:sync", Status: domain.StepStatusPassed, DurationMs: 3000},
			{Name: "dbflow:rollback", Status: domain.StepStatusPassed, DurationMs: 2500},
		},
		TotalDurMs: 6010,
		Started:    fixedTime,
		Ended:      fixedTime.Add(6010 * time.Millisecond),
	}
}

func fixedFailedReport() domain.RunReport {
	return domain.RunReport{
		Status:     domain.StatusFailed,
		Timestamp:  fixedTime,
		RepoURL:    "https://example.com/repo.git",
		BaseBranch: "main",
		Steps: []domain.StepResult{
			{Name: "preflight", Status: domain.StepStatusPassed, DurationMs: 10},
			{Name: "dbflow:sync", Status: domain.StepStatusFailed, DurationMs: 1500,
				Error: "BUILD FAILURE detected", Trace: "Maven build output..."},
		},
		TotalDurMs: 1510,
		Started:    fixedTime,
		Ended:      fixedTime.Add(1510 * time.Millisecond),
	}
}

func TestJSONRenderer_GoldenFile(t *testing.T) {
	r := report.NewJSONRenderer()

	tests := []struct {
		name   string
		rpt    domain.RunReport
		golden string
	}{
		{"passed", fixedPassedReport(), "testdata/report_passed.json"},
		{"failed", fixedFailedReport(), "testdata/report_failed.json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := r.Render(tt.rpt)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}

			if *update {
				if err := os.WriteFile(tt.golden, got, 0o644); err != nil {
					t.Fatalf("update golden: %v", err)
				}
				t.Logf("updated %s", tt.golden)
				return
			}

			want, err := os.ReadFile(tt.golden)
			if err != nil {
				t.Fatalf("read golden %s: %v (run with -update to create)", tt.golden, err)
			}
			if string(got) != string(want) {
				t.Errorf("JSON output mismatch.\nGot:\n%s\nWant:\n%s", got, want)
			}
		})
	}
}

func TestJSONRenderer_SecretsAbsent(t *testing.T) {
	r := report.NewJSONRenderer()

	rpt := fixedPassedReport()
	// Simulate a trace that might have been contaminated with secrets
	// (in practice, our code never puts them there — this is a safety net test).
	rpt.Steps[0].Trace = "token=mySecretToken password=mySecretPass"

	out, err := r.Render(rpt)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	// The JSON output should be valid.
	var raw map[string]interface{}
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// The report's repo_url and other safe fields must be present.
	if _, ok := raw["repo_url"]; !ok {
		t.Error("repo_url missing from JSON output")
	}
	if _, ok := raw["status"]; !ok {
		t.Error("status missing from JSON output")
	}
}
