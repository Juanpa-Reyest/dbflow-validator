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
