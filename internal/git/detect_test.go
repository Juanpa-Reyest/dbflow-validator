package git

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// fakeExec builds a DetectExecFunc that returns predefined outputs for specific commands.
// commands maps "git <args>" → "stdout|stderr|exitcode".
func fakeExec(commands map[string]string) DetectExecFunc {
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		key := name + " " + strings.Join(args, " ")
		response, ok := commands[key]
		if !ok {
			// Command not found in map → produce a failing command.
			return exec.CommandContext(ctx, "false")
		}

		parts := strings.SplitN(response, "|", 3)
		stdout := parts[0]
		// Build a command that echoes the expected stdout.
		// Use "echo" with -n to avoid trailing newline issues.
		if stdout == "" {
			return exec.CommandContext(ctx, "true")
		}
		return exec.CommandContext(ctx, "echo", "-n", stdout)
	}
}

// helperExec builds a DetectExecFunc where each command key maps to a helper
// with controlled stdout, stderr, and exit code. This is more reliable than
// the echo-based approach for commands that need to fail.
type fakeCmd struct {
	stdout   string
	stderr   string
	exitCode int
}

func fakeExecMap(commands map[string]fakeCmd) DetectExecFunc {
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		key := name + " " + strings.Join(args, " ")
		fc, ok := commands[key]
		if !ok {
			// Unknown command → fail.
			cmd := exec.CommandContext(ctx, "sh", "-c", "exit 1")
			return cmd
		}
		if fc.exitCode != 0 {
			script := fmt.Sprintf("echo -n '%s' >&2; exit %d", fc.stderr, fc.exitCode)
			return exec.CommandContext(ctx, "sh", "-c", script)
		}
		// Success case: write stdout.
		cmd := exec.CommandContext(ctx, "echo", "-n", fc.stdout)
		return cmd
	}
}

func TestDetectRemoteURL_Success(t *testing.T) {
	commands := map[string]fakeCmd{
		"git remote get-url origin": {stdout: "https://github.com/org/repo.git", exitCode: 0},
	}

	url, err := DetectRemoteURL(context.Background(), fakeExecMap(commands))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://github.com/org/repo.git" {
		t.Errorf("got %q, want %q", url, "https://github.com/org/repo.git")
	}
}

func TestDetectRemoteURL_SSH(t *testing.T) {
	commands := map[string]fakeCmd{
		"git remote get-url origin": {stdout: "git@github.com:org/repo.git", exitCode: 0},
	}

	url, err := DetectRemoteURL(context.Background(), fakeExecMap(commands))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "git@github.com:org/repo.git" {
		t.Errorf("got %q, want %q", url, "git@github.com:org/repo.git")
	}
}

func TestDetectRemoteURL_NoOrigin(t *testing.T) {
	commands := map[string]fakeCmd{
		"git remote get-url origin": {stderr: "fatal: No such remote 'origin'", exitCode: 1},
	}

	_, err := DetectRemoteURL(context.Background(), fakeExecMap(commands))
	if err == nil {
		t.Fatal("expected error when no origin remote exists")
	}
	if !strings.Contains(err.Error(), "detect repo URL") {
		t.Errorf("error should mention 'detect repo URL', got: %v", err)
	}
}

func TestDetectBaseBranch_ViaUpstream(t *testing.T) {
	commands := map[string]fakeCmd{
		"git rev-parse --abbrev-ref @{upstream}": {stdout: "origin/prod", exitCode: 0},
	}

	branch, err := DetectBaseBranch(context.Background(), fakeExecMap(commands), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if branch != "prod" {
		t.Errorf("got %q, want %q", branch, "prod")
	}
}

func TestDetectBaseBranch_ViaMergeBase(t *testing.T) {
	// Upstream fails, but merge-base succeeds for "integration".
	commands := map[string]fakeCmd{
		"git rev-parse --abbrev-ref @{upstream}": {exitCode: 1},
		// main doesn't exist
		"git rev-parse --verify --quiet origin/main": {exitCode: 1},
		"git rev-parse --verify --quiet main":        {exitCode: 1},
		// master doesn't exist
		"git rev-parse --verify --quiet origin/master": {exitCode: 1},
		"git rev-parse --verify --quiet master":        {exitCode: 1},
		// develop doesn't exist
		"git rev-parse --verify --quiet origin/develop": {exitCode: 1},
		"git rev-parse --verify --quiet develop":        {exitCode: 1},
		// integration exists
		"git rev-parse --verify --quiet origin/integration": {stdout: "abc123", exitCode: 0},
		"git merge-base HEAD origin/integration":            {stdout: "abc123", exitCode: 0},
		"git rev-list --count abc123..HEAD":                 {stdout: "3", exitCode: 0},
		// prod exists but is further away
		"git rev-parse --verify --quiet origin/prod": {stdout: "def456", exitCode: 0},
		"git merge-base HEAD origin/prod":            {stdout: "def456", exitCode: 0},
		"git rev-list --count def456..HEAD":          {stdout: "10", exitCode: 0},
	}

	branch, err := DetectBaseBranch(context.Background(), fakeExecMap(commands), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if branch != "integration" {
		t.Errorf("got %q, want %q (closest merge-base)", branch, "integration")
	}
}

func TestDetectBaseBranch_NoCandidatesFound(t *testing.T) {
	commands := map[string]fakeCmd{
		"git rev-parse --abbrev-ref @{upstream}": {exitCode: 1},
	}
	// All candidates will fail (not in the map).

	_, err := DetectBaseBranch(context.Background(), fakeExecMap(commands), []string{"main", "prod"})
	if err == nil {
		t.Fatal("expected error when no candidate branch is found")
	}
	if !strings.Contains(err.Error(), "could not determine base branch") {
		t.Errorf("error should mention heuristic failure, got: %v", err)
	}
}

// Ensure unused imports don't break compilation.
var _ = bytes.Buffer{}
