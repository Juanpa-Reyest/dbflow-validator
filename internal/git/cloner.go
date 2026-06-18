// Package git provides a git clone adapter for the dbflow-validator.
// Token auth is performed by injecting the token into the HTTPS URL in memory.
// Logged arguments always use the redacted form; the raw token is never logged.
// SSH URLs are cloned as-is using the host's SSH agent/keys — no token needed.
package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
	"github.com/dbflow-validator/dbflow-validator/internal/giturl"
)

// ExecFunc is the injectable exec.CommandContext factory used in the cloner.
// Inject a fake in tests; pass nil to use the real exec.CommandContext.
type ExecFunc func(ctx context.Context, name string, args ...string) *exec.Cmd

// MkdirAllFunc is the injectable directory-creation function.
type MkdirAllFunc func(path string, perm os.FileMode) error

// GitCloner clones remote git repositories into a local 0700 temp directory.
// It satisfies domain.Cloner.
type GitCloner struct {
	execFn    ExecFunc
	mkdirFn   MkdirAllFunc
}

// NewCloner returns a GitCloner with injectable dependencies.
// Pass nil for execFn to use the real exec.CommandContext.
// Pass nil for mkdirFn to use the real os.MkdirAll.
func NewCloner(execFn ExecFunc, mkdirFn MkdirAllFunc) *GitCloner {
	if execFn == nil {
		execFn = func(ctx context.Context, name string, args ...string) *exec.Cmd {
			return exec.CommandContext(ctx, name, args...)
		}
	}
	if mkdirFn == nil {
		mkdirFn = os.MkdirAll
	}
	return &GitCloner{execFn: execFn, mkdirFn: mkdirFn}
}

// Clone clones opts.RepoURL at opts.Branch into opts.DestDir.
// The directory is created with 0700 permissions before cloning.
// Token authentication is done by rewriting the URL in memory — the raw token
// never appears in any log line or error message.
//
// After cloning, Clone validates that the required archetype paths exist:
//   - src/main/resources/db/liquibase.properties
//   - src/main/resources/db/schema/master-changelog/ (directory)
func (c *GitCloner) Clone(ctx context.Context, opts domain.CloneOptions) (string, error) {
	// Create destination directory with restrictive permissions.
	if err := c.mkdirFn(opts.DestDir, 0o700); err != nil {
		return "", fmt.Errorf("create clone dir: %w", err)
	}

	// Build the authenticated URL. The token is revealed exactly once here,
	// only to build the real URL passed to git. Log output uses the redacted form.
	realURL := opts.RepoURL
	rawToken := opts.Token.Reveal()
	if rawToken != "" {
		realURL = injectToken(opts.RepoURL, rawToken)
	}

	// Run: git clone --branch <branch> --depth 1 <url> <dest>
	args := []string{"clone", "--branch", opts.Branch, "--depth", "1", realURL, opts.DestDir}
	cmd := c.execFn(ctx, "git", args...)
	cmd.Stdout = nil
	// Capture git's stderr into a buffer so we can include it in the error message.
	// This makes failures like "remote branch X not found" visible instead of the
	// bare "exit status 128". The buffer is safe to include: git does not echo the
	// injected token into stderr; only the redacted original URL is surfaced.
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Run(); err != nil {
		// Never include the real URL in the error message (it may contain the token).
		redactedURL := redactURL(opts.RepoURL)
		stderrText := strings.TrimSpace(stderrBuf.String())
		if stderrText != "" {
			return "", fmt.Errorf("%w: git clone %s: %v: %s", domain.ErrCloneFailed, redactedURL, err, stderrText)
		}
		return "", fmt.Errorf("%w: git clone %s: %v", domain.ErrCloneFailed, redactedURL, err)
	}

	// Validate required archetype structure.
	if err := validateStructure(opts.DestDir); err != nil {
		return "", err
	}

	return opts.DestDir, nil
}

// injectToken rewrites an HTTPS URL to embed x-access-token:<token>@ for git auth.
// Input:  "https://github.com/org/repo.git"
// Output: "https://x-access-token:<token>@github.com/org/repo.git"
func injectToken(repoURL, token string) string {
	const httpsPrefix = "https://"
	if strings.HasPrefix(repoURL, httpsPrefix) {
		rest := strings.TrimPrefix(repoURL, httpsPrefix)
		return httpsPrefix + "x-access-token:" + token + "@" + rest
	}
	return repoURL
}

// redactURL returns a URL safe for logging.
//
//   - SSH URLs carry no embedded secret: returned as-is.
//   - HTTPS URLs: the original URL (without injected token) is returned as-is;
//     the token-injected form is never passed to this function.
//   - Any other form with an embedded "@" (e.g. user:pass@host) is redacted.
func redactURL(repoURL string) string {
	// SSH URLs have no embedded credentials — safe to log verbatim.
	if giturl.IsSSHURL(repoURL) {
		return repoURL
	}
	// HTTPS: the caller always passes the original URL (no injected token), safe to log.
	if strings.HasPrefix(repoURL, "https://") || strings.HasPrefix(repoURL, "http://") {
		return repoURL
	}
	// Unknown scheme with potential credentials — redact conservatively.
	return "(redacted-url)"
}

// validateStructure checks that the cloned repo has the expected archetype layout.
func validateStructure(cloneRoot string) error {
	propsPath := cloneRoot + "/src/main/resources/db/liquibase.properties"
	if _, err := os.Stat(propsPath); err != nil {
		return fmt.Errorf("archetype structure invalid: missing liquibase.properties at %s", propsPath)
	}

	clDir := cloneRoot + "/src/main/resources/db/schema/master-changelog"
	info, err := os.Stat(clDir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("archetype structure invalid: missing master-changelog directory at %s", clDir)
	}

	return nil
}

// Ensure GitCloner satisfies domain.Cloner at compile time.
var _ domain.Cloner = (*GitCloner)(nil)
