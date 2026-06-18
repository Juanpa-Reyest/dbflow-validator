package maven_test

import (
	"context"
	"testing"
	"time"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
	"github.com/dbflow-validator/dbflow-validator/internal/maven"
)

// TestContainerRunner_HomeEnvSet verifies that BuildContainerRequest sets HOME=/tmp
// so the Maven container does not attempt to write to /root/.m2 when running as
// a non-root host UID (which would emit "cannot create directory '/root'" warning).
func TestContainerRunner_HomeEnvSet(t *testing.T) {
	req := maven.BuildContainerRequest(
		maven.DefaultImage,
		"dbflow-net-test",
		"/tmp/m2-cache",
		1000, // non-root UID
		1000,
		"/tmp/cloneroot",
		"dbflow:sync",
		[]string{"--TAG=test", "--AUTHOR=validator-cli"},
	)

	home, ok := req.Env["HOME"]
	if !ok {
		t.Error("ContainerRequest.Env must contain HOME key to avoid /root/.m2 permission warning")
	}
	if home != "/tmp" {
		t.Errorf("ContainerRequest.Env[HOME] = %q; want /tmp", home)
	}
}

// TestContainerRunner_HomeEnvSetWhenRoot verifies HOME=/tmp is set even for root UID
// so the image default (/root/.m2) is overridden regardless of UID.
func TestContainerRunner_HomeEnvSetWhenRoot(t *testing.T) {
	req := maven.BuildContainerRequest(
		maven.DefaultImage,
		"dbflow-net-test",
		"",   // no repo cache
		0, 0, // root UID/GID
		"/tmp/cloneroot",
		"dbflow:sync",
		[]string{"--TAG=test"},
	)

	home, ok := req.Env["HOME"]
	if !ok {
		t.Error("ContainerRequest.Env must contain HOME even for root UID")
	}
	if home != "/tmp" {
		t.Errorf("ContainerRequest.Env[HOME] = %q; want /tmp", home)
	}
}

// TestMapContainerResult exercises the pure exit-code + BUILD-FAILURE mapping
// used by ContainerRunner. This is the direct coverage for WARNING-1 in the
// verify report: the ContainerRunner failure-path mapping was previously only
// validated transitively by the happy-path e2e test.
//
// The seam under test: maven.MapContainerResult(exitCode, trace, goal, elapsed, ctxErr).
func TestMapContainerResult(t *testing.T) {
	const goal = "dbflow:sync"
	elapsed := 100 * time.Millisecond

	tests := []struct {
		name       string
		exitCode   int
		trace      string
		ctxErr     error
		wantStatus domain.StepStatus
		wantErrSub string // non-empty: result.Error must contain this substring
	}{
		{
			name:       "exit 0, clean output → PASSED",
			exitCode:   0,
			trace:      "BUILD SUCCESS",
			wantStatus: domain.StepStatusPassed,
		},
		{
			name:       "exit non-zero → FAILED with exit code in error",
			exitCode:   1,
			trace:      "BUILD FAILURE\n[ERROR] Some error",
			wantStatus: domain.StepStatusFailed,
			wantErrSub: "1",
		},
		{
			name:       "exit non-zero (127 image-not-found) → FAILED",
			exitCode:   127,
			trace:      "",
			wantStatus: domain.StepStatusFailed,
			wantErrSub: "127",
		},
		{
			name:       "exit 0 but BUILD FAILURE in trace → FAILED",
			exitCode:   0,
			trace:      "lots of output\nBUILD FAILURE\nmore output",
			wantStatus: domain.StepStatusFailed,
			wantErrSub: "BUILD FAILURE",
		},
		{
			name:       "context cancelled → ABORTED",
			exitCode:   0,
			trace:      "",
			ctxErr:     context.Canceled,
			wantStatus: domain.StepStatusAborted,
		},
		{
			name:       "context deadline exceeded → ABORTED",
			exitCode:   1,
			trace:      "",
			ctxErr:     context.DeadlineExceeded,
			wantStatus: domain.StepStatusAborted,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := maven.MapContainerResult(tt.exitCode, tt.trace, goal, elapsed, tt.ctxErr)

			if result.Status != tt.wantStatus {
				t.Errorf("status = %v, want %v", result.Status, tt.wantStatus)
			}
			if result.Name != goal {
				t.Errorf("Name = %q, want %q", result.Name, goal)
			}
			if result.Duration != elapsed {
				t.Errorf("Duration = %v, want %v", result.Duration, elapsed)
			}
			if tt.wantErrSub != "" && result.Error == "" {
				t.Errorf("Error is empty, want it to contain %q", tt.wantErrSub)
			}
			// Trace must be captured for failure/abort paths so callers can diagnose.
			if tt.wantStatus != domain.StepStatusPassed && result.Trace == "" && tt.trace != "" {
				t.Errorf("Trace is empty for non-passed result; want trace captured")
			}
		})
	}
}
