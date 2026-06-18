package rundir_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dbflow-validator/dbflow-validator/internal/rundir"
)

func TestRunDirPath(t *testing.T) {
	fixedTime := time.Date(2024, 3, 15, 9, 5, 7, 0, time.UTC)

	tests := []struct {
		name      string
		outputDir string
		ts        time.Time
		wantSuffix string
		wantContains string
	}{
		{
			name:         "timestamp format is 20060102T150405Z",
			outputDir:    "/tmp/runs",
			ts:           fixedTime,
			wantSuffix:   "20240315T090507Z",
			wantContains: "20240315T090507Z",
		},
		{
			name:         "absolute output dir joined with timestamp",
			outputDir:    "/tmp/my-runs",
			ts:           fixedTime,
			wantContains: "/tmp/my-runs/20240315T090507Z",
		},
		{
			name:         "output dir and timestamp combined correctly",
			outputDir:    "/abs/path",
			ts:           time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC),
			wantContains: "/abs/path/20251231T235959Z",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rundir.RunDirPath(tt.outputDir, tt.ts)

			if tt.wantContains != "" && !strings.Contains(got, tt.wantContains) {
				t.Errorf("RunDirPath(%q, %v) = %q; want it to contain %q",
					tt.outputDir, tt.ts, got, tt.wantContains)
			}

			// Verify timestamp suffix format: exactly 16 chars "20060102T150405Z"
			base := filepath.Base(got)
			if len(base) != 16 {
				t.Errorf("timestamp dir name %q has length %d; want 16", base, len(base))
			}
			if !strings.HasSuffix(base, "Z") {
				t.Errorf("timestamp dir name %q must end with Z (UTC marker)", base)
			}
			if !strings.Contains(base, "T") {
				t.Errorf("timestamp dir name %q must contain T separator", base)
			}
		})
	}
}

func TestRunDirPath_RelativeOutputDir(t *testing.T) {
	// Relative output dirs are passed as-is (caller resolves them relative to cwd
	// before calling RunDirPath — this matches the design).
	ts := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	got := rundir.RunDirPath("dbflow-validator-runs", ts)
	want := filepath.Join("dbflow-validator-runs", "20240102T030405Z")
	if got != want {
		t.Errorf("RunDirPath(relative) = %q, want %q", got, want)
	}
}
