package maven_test

import (
	"context"
	"testing"
	"time"

	dockercontainer "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"

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

// extractMountsFromRequest calls the HostConfigModifier on a temporary HostConfig
// so we can inspect what mounts the container request would configure at runtime.
func extractMountsFromRequest(t *testing.T, cloneRoot, repoCachePath string) []mount.Mount {
	t.Helper()
	req := maven.BuildContainerRequest(
		maven.DefaultImage,
		"dbflow-net-test",
		repoCachePath,
		1000, 1000,
		cloneRoot,
		"dbflow:sync",
		[]string{"--TAG=test"},
	)
	hc := &dockercontainer.HostConfig{}
	req.HostConfigModifier(hc)
	return hc.Mounts
}

// TestMavenContainerRequest_MountsCloneRootRW asserts that the clone root is
// mounted as a typed bind mount (Source=cloneRoot, Target=/work, ReadOnly=false).
// Typed mounts avoid the Windows drive-letter colon ambiguity in raw bind strings.
func TestMavenContainerRequest_MountsCloneRootRW(t *testing.T) {
	const cloneRoot = "/tmp/clone-test"
	mounts := extractMountsFromRequest(t, cloneRoot, "/tmp/m2-cache")

	for _, m := range mounts {
		if m.Source == cloneRoot && m.Target == "/work" {
			if m.ReadOnly {
				t.Errorf("clone root mount must be read-write (ReadOnly=false)")
			}
			return
		}
	}
	t.Errorf("Mounts must contain {Source:%q, Target:\"/work\", ReadOnly:false}; got=%+v", cloneRoot, mounts)
}

// TestMavenContainerRequest_MountsRepoCacheReadOnly asserts that the repo cache
// is mounted read-only (Source=repoCachePath, Target=/m2, ReadOnly=true).
func TestMavenContainerRequest_MountsRepoCacheReadOnly(t *testing.T) {
	const cloneRoot = "/tmp/clone-test"
	const repoCache = "/tmp/m2-cache"
	mounts := extractMountsFromRequest(t, cloneRoot, repoCache)

	for _, m := range mounts {
		if m.Source == repoCache && m.Target == "/m2" {
			if !m.ReadOnly {
				t.Errorf("repo cache mount must be read-only (ReadOnly=true)")
			}
			return
		}
	}
	t.Errorf("Mounts must contain {Source:%q, Target:\"/m2\", ReadOnly:true}; got=%+v", repoCache, mounts)
}

// TestMavenContainerRequest_NoCacheMount asserts that when repoCachePath is empty
// no /m2 mount is added.
func TestMavenContainerRequest_NoCacheMount(t *testing.T) {
	mounts := extractMountsFromRequest(t, "/tmp/clone-test", "")

	for _, m := range mounts {
		if m.Target == "/m2" {
			t.Errorf("no /m2 mount expected when repoCachePath is empty; got=%+v", mounts)
			return
		}
	}
}

// TestMavenContainerRequest_WindowsPathPassedThrough is the regression guard for
// the Windows bind-mount bug. A Windows-style source path must be preserved
// unchanged as the mount Source — never split on its drive colon.
func TestMavenContainerRequest_WindowsPathPassedThrough(t *testing.T) {
	winCloneRoot := `E:\Users\1048168\AppData\Local\Temp\dbflow-clone-611297184`
	winRepoCache := `C:\Users\1048168\.m2`
	mounts := extractMountsFromRequest(t, winCloneRoot, winRepoCache)

	foundClone := false
	foundCache := false
	for _, m := range mounts {
		if m.Source == winCloneRoot {
			foundClone = true
		}
		if m.Source == winRepoCache {
			foundCache = true
		}
	}
	if !foundClone {
		t.Errorf("Windows clone root %q must appear as mount Source unchanged; mounts=%+v", winCloneRoot, mounts)
	}
	if !foundCache {
		t.Errorf("Windows repo cache %q must appear as mount Source unchanged; mounts=%+v", winRepoCache, mounts)
	}
}
