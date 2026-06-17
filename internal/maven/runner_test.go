package maven_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
	"github.com/dbflow-validator/dbflow-validator/internal/maven"
)

// --- FormatParams tests ---

func TestFormatParams(t *testing.T) {
	tests := []struct {
		name  string
		pairs []maven.KV
		want  string
	}{
		{
			name: "sync params produce space-separated string",
			pairs: []maven.KV{
				{Key: "--TAG", Value: "myrun-001"},
				{Key: "--AUTHOR", Value: "validator-cli"},
			},
			want: "--TAG=myrun-001 --AUTHOR=validator-cli",
		},
		{
			name: "rollback params include TAG only (plugin uses standard rollback by default)",
			pairs: []maven.KV{
				{Key: "--TAG", Value: "210"},
			},
			want: "--TAG=210",
		},
		{
			name: "ordering is stable (as provided)",
			pairs: []maven.KV{
				{Key: "--B", Value: "2"},
				{Key: "--A", Value: "1"},
			},
			want: "--B=2 --A=1",
		},
		{
			name:  "single pair has no trailing space",
			pairs: []maven.KV{{Key: "--TAG", Value: "solo"}},
			want:  "--TAG=solo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := maven.FormatParams(tt.pairs)
			if got != tt.want {
				t.Errorf("FormatParams() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- Exit-code and BUILD FAILURE mapping tests ---

// writeFakeMvn creates a tiny shell script that exits with the given code and
// optionally prints "BUILD FAILURE" to stdout. Returns the script path.
func writeFakeMvn(t *testing.T, exitCode int, buildFailure bool) string {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "mvn")

	output := ""
	if buildFailure {
		output = "echo 'BUILD FAILURE'"
	}
	content := fmt.Sprintf("#!/bin/sh\n%s\nexit %d\n", output, exitCode)
	if err := os.WriteFile(script, []byte(content), 0o700); err != nil {
		t.Fatalf("write fake mvn: %v", err)
	}
	return script
}

func TestMavenRunner_Run(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping exec-based tests in -short mode")
	}

	tests := []struct {
		name         string
		exitCode     int
		buildFailure bool
		cancelCtx    bool
		wantStatus   domain.StepStatus
	}{
		{
			name:       "exit 0 → StepStatusPassed",
			exitCode:   0,
			wantStatus: domain.StepStatusPassed,
		},
		{
			name:       "exit non-zero → StepStatusFailed",
			exitCode:   1,
			wantStatus: domain.StepStatusFailed,
		},
		{
			name:         "BUILD FAILURE in stdout → StepStatusFailed even on exit 0",
			exitCode:     0,
			buildFailure: true,
			wantStatus:   domain.StepStatusFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeMvn := writeFakeMvn(t, tt.exitCode, tt.buildFailure)
			cloneDir := t.TempDir()

			// Write a minimal pom.xml so the runner can reference it.
			pom := filepath.Join(cloneDir, "pom.xml")
			if err := os.WriteFile(pom, []byte("<project/>"), 0o600); err != nil {
				t.Fatalf("write pom: %v", err)
			}

			runner := maven.NewRunner(fakeMvn, "")
			var out bytes.Buffer
			result, err := runner.Run(
				context.Background(),
				cloneDir,
				maven.GoalSync,
				[]string{"--TAG=test-001", "--AUTHOR=validator-cli"},
				&out,
			)
			if err != nil && tt.wantStatus != domain.StepStatusAborted {
				t.Fatalf("Run() unexpected error: %v", err)
			}
			if result.Status != tt.wantStatus {
				t.Errorf("status = %v, want %v\noutput:\n%s", result.Status, tt.wantStatus, out.String())
			}
		})
	}
}

func TestMavenRunner_CtxCancel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping exec-based tests in -short mode")
	}

	// A fake mvn that sleeps for 10 seconds.
	dir := t.TempDir()
	script := filepath.Join(dir, "mvn")
	content := "#!/bin/sh\nsleep 10\n"
	if err := os.WriteFile(script, []byte(content), 0o700); err != nil {
		t.Fatalf("write slow fake mvn: %v", err)
	}

	cloneDir := t.TempDir()
	pom := filepath.Join(cloneDir, "pom.xml")
	if err := os.WriteFile(pom, []byte("<project/>"), 0o600); err != nil {
		t.Fatalf("write pom: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	runner := maven.NewRunner(script, "")
	var out bytes.Buffer
	result, _ := runner.Run(ctx, cloneDir, maven.GoalSync, nil, &out)
	if result.Status != domain.StepStatusAborted {
		t.Errorf("cancelled ctx: status = %v, want StepStatusAborted", result.Status)
	}
}

func TestMavenRunner_TagUnique(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping exec-based tests in -short mode")
	}
	// Two runs should produce different TAG values.
	tags := make([]string, 2)
	dir := t.TempDir()
	script := filepath.Join(dir, "mvn")
	// Script that prints its own argv to a file so we can inspect the tag.
	outFile := filepath.Join(dir, "args.txt")
	content := fmt.Sprintf("#!/bin/sh\necho \"$@\" >> %s\n", outFile)
	if err := os.WriteFile(script, []byte(content), 0o700); err != nil {
		t.Fatalf("write recording mvn: %v", err)
	}

	cloneDir := t.TempDir()
	pom := filepath.Join(cloneDir, "pom.xml")
	if err := os.WriteFile(pom, []byte("<project/>"), 0o600); err != nil {
		t.Fatalf("write pom: %v", err)
	}

	runner := maven.NewRunner(script, "")
	for range tags {
		runner.Run(context.Background(), cloneDir, maven.GoalSync,
			[]string{"--AUTHOR=validator-cli"}, &bytes.Buffer{})
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read args file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected 2 arg lines, got %d", len(lines))
	}
	if lines[0] == lines[1] {
		t.Error("two runs produced identical argv — TAG is not unique")
	}
}
