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
	"path/filepath"
	"strings"

	"github.com/moby/term"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
	"github.com/dbflow-validator/dbflow-validator/internal/giturl"
)

const (
	tokenEnvVar      = "DBFLOW_GIT_TOKEN"
	defaultBranch    = "integration"
	defaultFormat    = "console"
	defaultLogLvl    = "info"
	defaultSQLInput  = "src/main/resources/SQLInput"
	defaultOutputDir = "dbflow-validator-runs"
)

// Config holds all resolved inputs for a validation run.
type Config struct {
	RepoURL      string
	BaseBranch   string
	// SQLInputPath is the absolute path to the developer's local SQLInput directory.
	// Resolved from the --sql-input flag (or its default) at parse time using os.Getwd().
	SQLInputPath string
	OutputFormat string
	OutputFile   string
	LogLevel     string
	// Token is stored as a Secret so it never leaks via fmt or JSON.
	Token domain.Secret
	// OutputDir is the absolute path to the directory where per-run artifact
	// subdirectories are created. Resolved from --output-dir (default:
	// ./dbflow-validator-runs relative to the working directory at parse time).
	OutputDir string
	// KeepWorkspace, when true, retains the ephemeral clone under <run>/workspace/
	// even on a PASSED run. Normally the clone is removed on success.
	KeepWorkspace bool
}

// String returns a human-readable representation that NEVER includes the token value.
func (c Config) String() string {
	return fmt.Sprintf(
		"Config{RepoURL:%q BaseBranch:%q SQLInputPath:%q OutputFormat:%q OutputFile:%q LogLevel:%q Token:%s OutputDir:%q KeepWorkspace:%v}",
		c.RepoURL, c.BaseBranch, c.SQLInputPath, c.OutputFormat, c.OutputFile, c.LogLevel, c.Token, c.OutputDir, c.KeepWorkspace,
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
	url := sanitizeRepoURL(scanner.Text())
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
	raw := sanitizeToken(scanner.Text())
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
	raw := sanitizeToken(scanner.Text())
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
		repoURL       string
		baseBranch    string
		sqlInputPath  string
		outputFormat  string
		outputFile    string
		logLevel      string
		outputDir     string
		keepWorkspace bool
	)

	fs.StringVar(&repoURL, "repo-url", "", "Repository URL to clone and validate")
	fs.StringVar(&baseBranch, "base-branch", defaultBranch, "Branch to validate (default: integration)")
	fs.StringVar(&sqlInputPath, "sql-input", "", "Path to local SQLInput directory (default: ./src/main/resources/SQLInput)")
	fs.StringVar(&outputFormat, "output-format", defaultFormat, "Output format: console or json (default: console)")
	fs.StringVar(&outputFile, "output-file", "", "Path to write JSON output (optional)")
	fs.StringVar(&logLevel, "log-level", defaultLogLvl, "Log level: debug, info, warn, error (default: info)")
	fs.StringVar(&outputDir, "output-dir", "", "Directory for per-run artifact subdirectories (default: ./dbflow-validator-runs)")
	fs.BoolVar(&keepWorkspace, "keep-workspace", false, "Retain the ephemeral clone under <run>/workspace/ even on a PASSED run")

	// Discard usage output; callers handle errors themselves.
	var usageBuf strings.Builder
	fs.SetOutput(&usageBuf)

	if err := fs.Parse(args); err != nil {
		return Config{}, fmt.Errorf("flag parse: %w", err)
	}

	// Resolve repo URL: flag > prompt.
	// Defensively sanitize the flag value too — shell completion or copy-paste
	// may inject ANSI sequences even in non-interactive mode.
	repoURL = sanitizeRepoURL(repoURL)
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

	// Resolve SQLInputPath: use flag value if provided, otherwise default relative to cwd.
	resolvedSQLInput := sqlInputPath
	if resolvedSQLInput == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return Config{}, fmt.Errorf("resolve cwd for --sql-input default: %w", err)
		}
		resolvedSQLInput = filepath.Join(cwd, defaultSQLInput)
	} else if !filepath.IsAbs(resolvedSQLInput) {
		cwd, err := os.Getwd()
		if err != nil {
			return Config{}, fmt.Errorf("resolve cwd for --sql-input: %w", err)
		}
		resolvedSQLInput = filepath.Join(cwd, resolvedSQLInput)
	}

	// Resolve OutputDir: use flag value if provided, otherwise default relative to cwd.
	resolvedOutputDir := outputDir
	if resolvedOutputDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return Config{}, fmt.Errorf("resolve cwd for --output-dir default: %w", err)
		}
		resolvedOutputDir = filepath.Join(cwd, defaultOutputDir)
	} else if !filepath.IsAbs(resolvedOutputDir) {
		cwd, err := os.Getwd()
		if err != nil {
			return Config{}, fmt.Errorf("resolve cwd for --output-dir: %w", err)
		}
		resolvedOutputDir = filepath.Join(cwd, resolvedOutputDir)
	}

	return Config{
		RepoURL:       repoURL,
		BaseBranch:    baseBranch,
		SQLInputPath:  resolvedSQLInput,
		OutputFormat:  outputFormat,
		OutputFile:    outputFile,
		LogLevel:      logLevel,
		Token:         token,
		OutputDir:     resolvedOutputDir,
		KeepWorkspace: keepWorkspace,
	}, nil
}
