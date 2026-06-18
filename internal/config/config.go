// Package config resolves CLI flags and environment variables into a Config value.
// Precedence: flags > env > interactive prompt > defaults.
// The git token is read exclusively from DBFLOW_GIT_TOKEN or an interactive prompt
// — never from a flag, never written to disk, and never emitted in logs or String() output.
package config

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/moby/term"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
	"github.com/dbflow-validator/dbflow-validator/internal/giturl"
)

const (
	tokenEnvVar   = "DBFLOW_GIT_TOKEN"
	defaultBranch = "integracion"
	defaultFormat = "console"
	defaultLogLvl = "info"
)

// Config holds all resolved inputs for a validation run.
type Config struct {
	RepoURL      string
	BaseBranch   string
	OutputFormat string
	OutputFile   string
	LogLevel     string
	// Token is stored as a Secret so it never leaks via fmt or JSON.
	Token domain.Secret
}

// String returns a human-readable representation that NEVER includes the token value.
func (c Config) String() string {
	return fmt.Sprintf(
		"Config{RepoURL:%q BaseBranch:%q OutputFormat:%q OutputFile:%q LogLevel:%q Token:%s}",
		c.RepoURL, c.BaseBranch, c.OutputFormat, c.OutputFile, c.LogLevel, c.Token,
	)
}

// PromptReader abstracts interactive terminal input so it can be replaced in tests.
type PromptReader interface {
	// ReadRepoURL prompts the user for the repository URL (visible input).
	ReadRepoURL() (string, error)
	// ReadToken prompts the user for the git access token (no-echo input).
	// The returned Secret wraps the raw token immediately; the raw string is never stored.
	ReadToken() (domain.Secret, error)
}

// DefaultPromptReader reads from stdin using terminal I/O.
// It suppresses echo for the token using github.com/moby/term.
// Call NewDefaultPromptReader to construct one.
type DefaultPromptReader struct {
	stdin *os.File
}

// NewDefaultPromptReader returns a DefaultPromptReader that reads from os.Stdin.
func NewDefaultPromptReader() *DefaultPromptReader {
	return &DefaultPromptReader{stdin: os.Stdin}
}

// ReadRepoURL prints a prompt to stderr and reads a line from stdin (visible).
func (r *DefaultPromptReader) ReadRepoURL() (string, error) {
	fmt.Fprint(os.Stderr, "Repository URL: ")
	scanner := bufio.NewScanner(r.stdin)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", fmt.Errorf("read repo URL: %w", err)
		}
		return "", fmt.Errorf("read repo URL: unexpected EOF")
	}
	url := strings.TrimSpace(scanner.Text())
	if url == "" {
		return "", fmt.Errorf("repository URL must not be empty")
	}
	return url, nil
}

// ReadToken prints a prompt to stderr and reads the token from stdin with echo suppressed.
// The raw token is wrapped in domain.Secret immediately and never stored as a plain string.
func (r *DefaultPromptReader) ReadToken() (domain.Secret, error) {
	fmt.Fprint(os.Stderr, "Git access token (hidden): ")

	fd := r.stdin.Fd()
	// Save terminal state, disable echo for the read, restore afterward.
	state, err := term.SaveState(fd)
	if err != nil {
		// Cannot save state — fall back to visible read (still wraps in Secret).
		return r.readTokenVisible()
	}
	if err := term.DisableEcho(fd, state); err != nil {
		return r.readTokenVisible()
	}
	defer func() {
		_ = term.RestoreTerminal(fd, state)
		fmt.Fprintln(os.Stderr) // newline after hidden input
	}()

	scanner := bufio.NewScanner(r.stdin)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return domain.Secret{}, fmt.Errorf("read token: %w", err)
		}
		return domain.Secret{}, fmt.Errorf("read token: unexpected EOF")
	}
	raw := scanner.Text()
	if raw == "" {
		return domain.Secret{}, fmt.Errorf("git access token must not be empty")
	}
	return domain.NewSecret(raw), nil
}

// readTokenVisible is a fallback used when the terminal state cannot be saved.
// It reads a line visibly but still wraps the result in a Secret.
func (r *DefaultPromptReader) readTokenVisible() (domain.Secret, error) {
	scanner := bufio.NewScanner(r.stdin)
	if !scanner.Scan() {
		return domain.Secret{}, fmt.Errorf("read token: unexpected EOF")
	}
	raw := strings.TrimSpace(scanner.Text())
	fmt.Fprintln(os.Stderr)
	if raw == "" {
		return domain.Secret{}, fmt.Errorf("git access token must not be empty")
	}
	return domain.NewSecret(raw), nil
}

// Resolve parses args and env; returns an error when required inputs are missing
// and no TTY interactive prompt is available (non-TTY path).
//
// This is a convenience wrapper around ResolveWithPrompter that passes a real
// DefaultPromptReader when stdin is a TTY, or nil when it is not.
func Resolve(args []string, env func(string) string) (Config, error) {
	var prompter PromptReader
	if term.IsTerminal(os.Stdin.Fd()) {
		prompter = NewDefaultPromptReader()
	}
	return ResolveWithPrompter(args, env, prompter)
}

// ResolveWithPrompter parses args and env; when repo URL or token are missing it
// falls back to prompter (if non-nil) to request them interactively.
// Precedence: flag > env > prompt.
//
// Returns an error when:
//   - flag parsing fails
//   - repo URL is missing and prompter is nil (non-TTY)
//   - DBFLOW_GIT_TOKEN is unset, prompter is nil, and there is no --repo-url flag
//   - any prompt read fails
func ResolveWithPrompter(args []string, env func(string) string, prompter PromptReader) (Config, error) {
	fs := flag.NewFlagSet("dbflow-validator", flag.ContinueOnError)

	var (
		repoURL      string
		baseBranch   string
		outputFormat string
		outputFile   string
		logLevel     string
	)

	fs.StringVar(&repoURL, "repo-url", "", "Repository URL to clone and validate")
	fs.StringVar(&baseBranch, "base-branch", defaultBranch, "Branch to validate (default: integracion)")
	fs.StringVar(&outputFormat, "output-format", defaultFormat, "Output format: console or json (default: console)")
	fs.StringVar(&outputFile, "output-file", "", "Path to write JSON output (optional)")
	fs.StringVar(&logLevel, "log-level", defaultLogLvl, "Log level: debug, info, warn, error (default: info)")

	// Discard usage output; callers handle errors themselves.
	var usageBuf strings.Builder
	fs.SetOutput(&usageBuf)

	if err := fs.Parse(args); err != nil {
		return Config{}, fmt.Errorf("flag parse: %w", err)
	}

	// Resolve repo URL: flag > prompt.
	if repoURL == "" {
		if prompter == nil {
			return Config{}, fmt.Errorf("--repo-url is required (or run interactively with a TTY)")
		}
		url, err := prompter.ReadRepoURL()
		if err != nil {
			return Config{}, fmt.Errorf("interactive prompt for repo-url: %w", err)
		}
		repoURL = url
	}

	// Resolve token: env > prompt.
	// SSH URLs (scp-style or ssh://) rely on the host SSH agent/keys and do NOT
	// require a personal access token — skip token resolution entirely for them.
	var token domain.Secret
	if !giturl.IsSSHURL(repoURL) {
		rawToken := env(tokenEnvVar)
		if rawToken != "" {
			token = domain.NewSecret(rawToken)
		} else {
			if prompter == nil {
				return Config{}, fmt.Errorf("%s environment variable is required; set it to your git access token (or run interactively with a TTY)", tokenEnvVar)
			}
			t, err := prompter.ReadToken()
			if err != nil {
				return Config{}, fmt.Errorf("interactive prompt for token: %w", err)
			}
			token = t
		}
	}

	return Config{
		RepoURL:      repoURL,
		BaseBranch:   baseBranch,
		OutputFormat: outputFormat,
		OutputFile:   outputFile,
		LogLevel:     logLevel,
		Token:        token,
	}, nil
}
