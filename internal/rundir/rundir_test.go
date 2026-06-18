package rundir_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dbflow-validator/dbflow-validator/internal/rundir"
)

// localTS creates a time in local timezone so that formatting it with .Local()
// produces the expected values regardless of the machine's TZ offset.
func localTS(year, month, day, hour, min, sec int) time.Time {
	return time.Date(year, time.Month(month), day, hour, min, sec, 0, time.Local)
}

func TestRunDirPath(t *testing.T) {
	tests := []struct {
		name         string
		outputDir    string
		ts           time.Time
		wantContains string
	}{
		{
			name:         "timestamp format is YYYY-MM-DD_HH-MM-SS",
			outputDir:    "/tmp/runs",
			ts:           localTS(2024, 3, 15, 9, 5, 7),
			wantContains: "2024-03-15_09-05-07",
		},
		{
			name:         "absolute output dir joined with timestamp",
			outputDir:    "/tmp/my-runs",
			ts:           localTS(2024, 3, 15, 9, 5, 7),
			wantContains: "/tmp/my-runs/2024-03-15_09-05-07",
		},
		{
			name:         "output dir and timestamp combined correctly",
			outputDir:    "/abs/path",
			ts:           localTS(2025, 12, 31, 23, 59, 59),
			wantContains: "/abs/path/2025-12-31_23-59-59",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rundir.RunDirPath(tt.outputDir, tt.ts)

			if tt.wantContains != "" && !strings.Contains(got, tt.wantContains) {
				t.Errorf("RunDirPath(%q, %v) = %q; want it to contain %q",
					tt.outputDir, tt.ts, got, tt.wantContains)
			}

			// Verify timestamp suffix format: exactly 19 chars "YYYY-MM-DD_HH-MM-SS"
			base := filepath.Base(got)
			if len(base) != 19 {
				t.Errorf("timestamp dir name %q has length %d; want 19", base, len(base))
			}
			// Must NOT contain T or Z (old ISO-8601 format).
			if strings.Contains(base, "T") {
				t.Errorf("timestamp dir name %q must NOT contain T separator (use _ instead)", base)
			}
			if strings.Contains(base, "Z") {
				t.Errorf("timestamp dir name %q must NOT contain Z UTC marker", base)
			}
			// Must use _ as date-time separator and - as time-component separator.
			if !strings.Contains(base, "_") {
				t.Errorf("timestamp dir name %q must contain _ date-time separator", base)
			}
		})
	}
}

func TestRunDirPath_RelativeOutputDir(t *testing.T) {
	// Relative output dirs are passed as-is (caller resolves them relative to cwd
	// before calling RunDirPath — this matches the design).
	ts := localTS(2024, 1, 2, 3, 4, 5)
	got := rundir.RunDirPath("dbflow-validator-runs", ts)
	want := filepath.Join("dbflow-validator-runs", "2024-01-02_03-04-05")
	if got != want {
		t.Errorf("RunDirPath(relative) = %q, want %q", got, want)
	}
}

func TestRunDirPath_ExampleFromDesign(t *testing.T) {
	// Design example: 2026-06-18_19-45-06
	ts := localTS(2026, 6, 18, 19, 45, 6)
	got := rundir.RunDirPath("/runs", ts)
	want := filepath.Join("/runs", "2026-06-18_19-45-06")
	if got != want {
		t.Errorf("RunDirPath(design example) = %q, want %q", got, want)
	}
}
