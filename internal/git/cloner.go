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
//
// The returned CommandTrace holds the exact (redacted) command line and the
// combined stdout+stderr produced by git. The trace is returned even on failure
// so callers can surface git's diagnostic output in the step trace.
// The adapter never scrubs secrets from the trace — the orchestrator does that
// via domain.ScrubSecrets before writing the trace to StepResult.Trace.
func (c *GitCloner) Clone(ctx context.Context, opts domain.CloneOptions) (string, domain.CommandTrace, error) {
	// Create destination directory with restrictive permissions.
	if err := c.mkdirFn(opts.DestDir, 0o700); err != nil {
		return "", domain.CommandTrace{}, fmt.Errorf("create clone dir: %w", err)
	}

	// Build the authenticated URL. The token is revealed exactly once here,
	// only to build the real URL passed to git. Log output uses the redacted form.
	realURL := opts.RepoURL
	rawToken := opts.Token.Reveal()
	if rawToken != "" {
		realURL = injectToken(opts.RepoURL, rawToken)
	}

	// Build the redacted command string for the trace (never contains the real token).
	redactedCmdArgs := []string{"git", "clone", "--branch", opts.Branch, "--depth", "1", redactURL(opts.RepoURL), opts.DestDir}
	redactedCmd := strings.Join(redactedCmdArgs, " ")

	// Run: git clone --branch <branch> --depth 1 <url> <dest>
	// Route both stdout and stderr into the same capture buffer so we surface
	// the full git output (progress on stderr, object counts on stdout) in the
	// execution.log trace. The buffer is NOT forwarded to os.Stderr so that
	// git's progress lines do not pollute the console.
	args := []string{"clone", "--branch", opts.Branch, "--depth", "1", realURL, opts.DestDir}
	cmd := c.execFn(ctx, "git", args...)
	var combinedBuf bytes.Buffer
	cmd.Stdout = &combinedBuf
	cmd.Stderr = &combinedBuf // both streams share the same buffer

	if err := cmd.Run(); err != nil {
		// Never include the real URL in the error message (it may contain the token).
		combinedText := strings.TrimSpace(combinedBuf.String())
		trace := domain.CommandTrace{Command: redactedCmd, Output: combinedText}
		if combinedText != "" {
			return "", trace, fmt.Errorf("%w: git clone %s: %v: %s", domain.ErrCloneFailed, redactURL(opts.RepoURL), err, combinedText)
		}
		return "", trace, fmt.Errorf("%w: git clone %s: %v", domain.ErrCloneFailed, redactURL(opts.RepoURL), err)
	}

	// Validate required archetype structure.
	if err := validateStructure(opts.DestDir); err != nil {
		trace := domain.CommandTrace{Command: redactedCmd, Output: strings.TrimSpace(combinedBuf.String())}
		return "", trace, err
	}

	trace := domain.CommandTrace{
		Command: redactedCmd,
		Output:  strings.TrimSpace(combinedBuf.String()),
	}
	return opts.DestDir, trace, nil
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
