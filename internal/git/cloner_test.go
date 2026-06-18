package git_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
	internalgit "github.com/dbflow-validator/dbflow-validator/internal/git"
)

func TestGitCloner_TokenRedaction(t *testing.T) {
	t.Run("error message does not contain raw token", func(t *testing.T) {
		// The fakeExec returns failure — Clone will produce an error.
		// The error message must NOT contain the raw token value.
		fakeExec := func(ctx context.Context, name string, args ...string) *exec.Cmd {
			// Simulate git failing.
			return exec.CommandContext(ctx, "false")
		}

		dir := t.TempDir()
		cloner := internalgit.NewCloner(fakeExec, os.MkdirAll)

		opts := domain.CloneOptions{
			RepoURL: "https://github.com/example/repo.git",
			Branch:  "main",
			Token:   domain.NewSecret("super-secret-token"),
			DestDir: filepath.Join(dir, "clone"),
		}

		_, err := cloner.Clone(context.Background(), opts)
		if err == nil {
			t.Fatal("expected clone to fail with fakeExec returning false")
		}

		// The error returned to the caller must NOT expose the raw token.
		if strings.Contains(err.Error(), "super-secret-token") {
			t.Errorf("raw token found in error message: %v", err)
		}
	})

	t.Run("clone dir created with perm 0700", func(t *testing.T) {
		fakeExec := func(ctx context.Context, name string, args ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "true")
		}

		dir := t.TempDir()
		destDir := filepath.Join(dir, "clone-target")
		cloner := internalgit.NewCloner(fakeExec, os.MkdirAll)

		opts := domain.CloneOptions{
			RepoURL: "https://github.com/example/repo.git",
			Branch:  "main",
			Token:   domain.NewSecret("tok"),
			DestDir: destDir,
		}

		_, _ = cloner.Clone(context.Background(), opts)

		info, err := os.Stat(destDir)
		if err != nil {
			t.Fatalf("expected dest dir to be created: %v", err)
		}
		perm := info.Mode().Perm()
		if perm != 0o700 {
			t.Errorf("expected perm 0700, got %04o", perm)
		}
	})

	t.Run("real URL with token passed to git command", func(t *testing.T) {
		var capturedArgs []string
		fakeExec := func(ctx context.Context, name string, args ...string) *exec.Cmd {
			capturedArgs = args
			return exec.CommandContext(ctx, "true")
		}

		dir := t.TempDir()
		cloner := internalgit.NewCloner(fakeExec, os.MkdirAll)

		opts := domain.CloneOptions{
			RepoURL: "https://github.com/example/repo.git",
			Branch:  "main",
			Token:   domain.NewSecret("my-real-token"),
			DestDir: filepath.Join(dir, "clone"),
		}

		_, _ = cloner.Clone(context.Background(), opts)

		// The authenticated URL must contain the token (passed to git, never to a logger).
		foundToken := false
		for _, arg := range capturedArgs {
			if strings.Contains(arg, "my-real-token") {
				foundToken = true
				break
			}
		}
		if !foundToken {
			t.Errorf("expected real token in git args, got: %v", capturedArgs)
		}
	})
}

// TestGitCloner_SSH verifies that SSH URLs clone as-is without token injection.
func TestGitCloner_SSH(t *testing.T) {
	t.Run("scp-style SSH URL: no token injected into git args", func(t *testing.T) {
		var capturedArgs []string
		fakeExec := func(ctx context.Context, name string, args ...string) *exec.Cmd {
			capturedArgs = args
			return exec.CommandContext(ctx, "true")
		}

		dir := t.TempDir()
		cloner := internalgit.NewCloner(fakeExec, os.MkdirAll)

		opts := domain.CloneOptions{
			RepoURL: "git@github.com:org/repo.git",
			Branch:  "main",
			Token:   domain.NewSecret(""), // no token for SSH
			DestDir: filepath.Join(dir, "clone"),
		}
		_, _ = cloner.Clone(context.Background(), opts)

		// The URL passed to git must be the original SSH URL — no token injection.
		found := false
		for _, arg := range capturedArgs {
			if arg == "git@github.com:org/repo.git" {
				found = true
			}
			// Must not contain any x-access-token injection.
			if strings.Contains(arg, "x-access-token") {
				t.Errorf("found unexpected token injection in git arg: %q", arg)
			}
		}
		if !found {
			t.Errorf("expected original SSH URL in git args, got: %v", capturedArgs)
		}
	})

	t.Run("ssh:// URL: no token injected into git args", func(t *testing.T) {
		var capturedArgs []string
		fakeExec := func(ctx context.Context, name string, args ...string) *exec.Cmd {
			capturedArgs = args
			return exec.CommandContext(ctx, "true")
		}

		dir := t.TempDir()
		cloner := internalgit.NewCloner(fakeExec, os.MkdirAll)

		opts := domain.CloneOptions{
			RepoURL: "ssh://git@github.com/org/repo.git",
			Branch:  "main",
			Token:   domain.NewSecret(""),
			DestDir: filepath.Join(dir, "clone"),
		}
		_, _ = cloner.Clone(context.Background(), opts)

		for _, arg := range capturedArgs {
			if strings.Contains(arg, "x-access-token") {
				t.Errorf("found unexpected token injection in git arg: %q", arg)
			}
		}
	})

	t.Run("SSH URL error message does not redact to misleading HTTPS string", func(t *testing.T) {
		fakeExec := func(ctx context.Context, name string, args ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "false") // simulate clone failure
		}

		dir := t.TempDir()
		cloner := internalgit.NewCloner(fakeExec, os.MkdirAll)

		opts := domain.CloneOptions{
			RepoURL: "git@github.com:org/repo.git",
			Branch:  "main",
			Token:   domain.NewSecret(""),
			DestDir: filepath.Join(dir, "clone"),
		}
		_, err := cloner.Clone(context.Background(), opts)
		if err == nil {
			t.Fatal("expected clone to fail")
		}
		// The SSH URL carries no secret — it should appear as-is, NOT as "https://***@(redacted)".
		errStr := err.Error()
		if strings.Contains(errStr, "***") || strings.Contains(errStr, "redacted") {
			t.Errorf("SSH URL error message incorrectly redacted: %v", errStr)
		}
		if !strings.Contains(errStr, "git@github.com") {
			t.Errorf("SSH URL should appear verbatim in error message, got: %v", errStr)
		}
	})

	t.Run("HTTPS URL error message still redacts (unchanged behavior)", func(t *testing.T) {
		fakeExec := func(ctx context.Context, name string, args ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "false")
		}

		dir := t.TempDir()
		cloner := internalgit.NewCloner(fakeExec, os.MkdirAll)

		opts := domain.CloneOptions{
			RepoURL: "https://github.com/org/repo.git",
			Branch:  "main",
			Token:   domain.NewSecret("secret-token"),
			DestDir: filepath.Join(dir, "clone"),
		}
		_, err := cloner.Clone(context.Background(), opts)
		if err == nil {
			t.Fatal("expected clone to fail")
		}
		if strings.Contains(err.Error(), "secret-token") {
			t.Errorf("raw token found in HTTPS error message: %v", err)
		}
	})
}

func TestGitCloner_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}

	// Create a local bare git repository with the required archetype structure.
	bareDir := t.TempDir()
	if err := exec.Command("git", "init", "--bare", bareDir).Run(); err != nil {
		t.Fatalf("init bare repo: %v", err)
	}

	// Create a working clone to populate the bare repo.
	workDir := t.TempDir()
	if err := exec.Command("git", "clone", bareDir, workDir).Run(); err != nil {
		t.Fatalf("clone bare: %v", err)
	}

	// Set git identity for the work clone.
	exec.Command("git", "-C", workDir, "config", "user.email", "test@example.com").Run()
	exec.Command("git", "-C", workDir, "config", "user.name", "Test").Run()

	// Create required archetype structure.
	propsDir := filepath.Join(workDir, "src", "main", "resources", "db")
	clDir := filepath.Join(propsDir, "schema", "master-changelog")
	if err := os.MkdirAll(clDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(propsDir, "liquibase.properties"), []byte("url=URL\nusername=U\npassword=P\ndriver=org.postgresql.Driver\n"), 0o644); err != nil {
		t.Fatalf("write props: %v", err)
	}
	if err := os.WriteFile(filepath.Join(clDir, "master-changelog.xml"), []byte(`<databaseChangeLog/>`), 0o644); err != nil {
		t.Fatalf("write changelog: %v", err)
	}

	// Commit and push to bare repo.
	exec.Command("git", "-C", workDir, "add", ".").Run()
	exec.Command("git", "-C", workDir, "commit", "-m", "init").Run()
	exec.Command("git", "-C", workDir, "push", "origin", "HEAD:main").Run()

	// Now clone using our real cloner (no token — local URL doesn't need one).
	destDir := t.TempDir()
	cloner := internalgit.NewCloner(nil, os.MkdirAll) // nil = use real exec.CommandContext

	opts := domain.CloneOptions{
		RepoURL: bareDir,
		Branch:  "main",
		Token:   domain.NewSecret(""), // empty token for local clone
		DestDir: filepath.Join(destDir, "clone"),
	}

	cloneRoot, err := cloner.Clone(context.Background(), opts)
	if err != nil {
		t.Fatalf("Clone error: %v", err)
	}

	// Assert required file exists.
	propsPath := filepath.Join(cloneRoot, "src", "main", "resources", "db", "liquibase.properties")
	if _, err := os.Stat(propsPath); err != nil {
		t.Errorf("expected liquibase.properties at %s: %v", propsPath, err)
	}
}
