// Package config resolves CLI flags and environment variables into a Config value.
// Precedence: flags > env > interactive prompt > defaults.
// The git token is read exclusively from DBFLOW_GIT_TOKEN or an interactive prompt
// — never from a flag, never written to disk, and never emitted in logs or String() output.
package config

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/moby/term"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
	"github.com/dbflow-validator/dbflow-validator/internal/git"
	"github.com/dbflow-validator/dbflow-validator/internal/giturl"
)

const (
	tokenEnvVar          = "DBFLOW_GIT_TOKEN"
	postgresImageEnvVar  = "DBFLOW_POSTGRES_IMAGE"
	defaultFormat        = "console"
	defaultLogLvl        = "info"
	defaultSQLInput      = "src/main/resources/SQLInput"
	defaultOutputDir     = "dbflow-validator-runs"
	defaultPostgresImage = "ghcr.io/juanpa-reyest/dbflow-postgres-partman:17.7"
)

// GitDetector abstracts auto-detection of repository URL and base branch from
// the current git working directory. This seam allows tests to inject a fake
// that does not depend on the real filesystem's git state.
type GitDetector interface {
	// DetectRemoteURL returns the origin remote URL of the cwd, or an error.
	DetectRemoteURL(ctx context.Context) (string, error)
	// DetectBaseBranch returns the inferred base branch, or an error.
	DetectBaseBranch(ctx context.Context) (string, error)
}

// RealGitDetector uses live git commands to detect repo URL and base branch.
type RealGitDetector struct{}

// DetectRemoteURL delegates to git.DetectRemoteURL with the real exec.
func (RealGitDetector) DetectRemoteURL(ctx context.Context) (string, error) {
	return git.DetectRemoteURL(ctx, nil)
}

// DetectBaseBranch delegates to git.DetectBaseBranch with defaults.
func (RealGitDetector) DetectBaseBranch(ctx context.Context) (string, error) {
	return git.DetectBaseBranch(ctx, nil, nil)
}

// Config holds all resolved inputs for a validation run.
type Config struct {
	RepoURL    string
	BaseBranch string
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
	// PostgresImage is the ephemeral Postgres container image to launch. Defaults to
	// defaultPostgresImage; override it to supply an image with extra extensions
	// (e.g. pg_partman). Resolved from --postgres-image or DBFLOW_POSTGRES_IMAGE.
	PostgresImage string
}

// String returns a human-readable representation that NEVER includes the token value.
func (c Config) String() string {
	return fmt.Sprintf(
		"Config{RepoURL:%q BaseBranch:%q SQLInputPath:%q OutputFormat:%q OutputFile:%q LogLevel:%q Token:%s OutputDir:%q KeepWorkspace:%v PostgresImage:%q}",
		c.RepoURL, c.BaseBranch, c.SQLInputPath, c.OutputFormat, c.OutputFile, c.LogLevel, c.Token, c.OutputDir, c.KeepWorkspace, c.PostgresImage,
	)
}

// PromptReader abstracts interactive terminal input so it can be replaced in tests.
type PromptReader interface {
	// ReadRepoURL prompts the user for the repository URL (visible input).
	ReadRepoURL() (string, error)
	// ReadBaseBranch prompts the user for the base branch to validate (visible input).
	ReadBaseBranch() (string, error)
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

// ReadBaseBranch prints a prompt to stderr and reads a line from stdin (visible).
func (r *DefaultPromptReader) ReadBaseBranch() (string, error) {
	fmt.Fprint(os.Stderr, "Base branch: ")
	scanner := bufio.NewScanner(r.stdin)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", fmt.Errorf("read base branch: %w", err)
		}
		return "", fmt.Errorf("read base branch: unexpected EOF")
	}
	branch := sanitizeBranch(scanner.Text())
	if branch == "" {
		return "", fmt.Errorf("base branch must not be empty")
	}
	return branch, nil
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
// It also passes RealGitDetector for auto-detection of repo URL and base branch.
func Resolve(args []string, env func(string) string) (Config, error) {
	var prompter PromptReader
	if term.IsTerminal(os.Stdin.Fd()) {
		prompter = NewDefaultPromptReader()
	}
	return ResolveWithPrompter(args, env, prompter, RealGitDetector{})
}

// ResolveWithPrompter parses args and env; when repo URL or token are missing it
// falls back to auto-detection (detector) and then to prompter (if non-nil).
// Precedence: flag > auto-detect > prompt.
//
// Pass nil for detector to disable auto-detection (useful in tests).
//
// Returns an error when:
//   - flag parsing fails
//   - repo URL is missing and both detector and prompter are nil/fail (non-TTY)
//   - DBFLOW_GIT_TOKEN is unset, prompter is nil, and there is no --repo-url flag
//   - any prompt read fails
func ResolveWithPrompter(args []string, env func(string) string, prompter PromptReader, detector GitDetector) (Config, error) {
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
		postgresImage string
	)

	fs.StringVar(&repoURL, "repo-url", "", "Repository URL to clone and validate")
	fs.StringVar(&baseBranch, "base-branch", "", "Branch to validate (required non-interactively; prompted when run interactively)")
	fs.StringVar(&sqlInputPath, "sql-input", "", "Path to local SQLInput directory (default: ./src/main/resources/SQLInput)")
	fs.StringVar(&outputFormat, "output-format", defaultFormat, "Output format: console or json (default: console)")
	fs.StringVar(&outputFile, "output-file", "", "Path to write JSON output (optional)")
	fs.StringVar(&logLevel, "log-level", defaultLogLvl, "Log level: debug, info, warn, error (default: info)")
	fs.StringVar(&outputDir, "output-dir", "", "Directory for per-run artifact subdirectories (default: ./dbflow-validator-runs)")
	fs.BoolVar(&keepWorkspace, "keep-workspace", false, "Retain the ephemeral clone under <run>/workspace/ even on a PASSED run")
	fs.StringVar(&postgresImage, "postgres-image", defaultPostgresImage, "Ephemeral Postgres container image (default: ghcr.io/juanpa-reyest/dbflow-postgres-partman:17.7; override for extra extensions such as pg_partman)")

	// Discard usage output; callers handle errors themselves.
	var usageBuf strings.Builder
	fs.SetOutput(&usageBuf)

	if err := fs.Parse(args); err != nil {
		return Config{}, fmt.Errorf("flag parse: %w", err)
	}

	// Track which flags were explicitly provided so env vars only fill in defaults.
	flagSet := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) { flagSet[f.Name] = true })

	// Resolve repo URL: flag > auto-detect > prompt.
	// Defensively sanitize the flag value too — shell completion or copy-paste
	// may inject ANSI sequences even in non-interactive mode.
	repoURL = sanitizeRepoURL(repoURL)
	if repoURL == "" {
		// Try auto-detection from the current git repository's origin remote.
		if detector != nil {
			if detected, err := detector.DetectRemoteURL(context.Background()); err == nil && detected != "" {
				repoURL = detected
				fmt.Fprintf(os.Stderr, "  Auto-detected repo: %s\n", repoURL)
			}
		}
		// If auto-detect didn't yield a result, fall through to prompt.
		if repoURL == "" {
			if prompter == nil {
				return Config{}, fmt.Errorf("--repo-url is required (auto-detection failed; or run interactively with a TTY)")
			}
			url, err := prompter.ReadRepoURL()
			if err != nil {
				return Config{}, fmt.Errorf("interactive prompt for repo-url: %w", err)
			}
			repoURL = url
		}
	}

	// Resolve base branch: flag > auto-detect > prompt.
	// Defensively sanitize the flag value too — shell completion or copy-paste
	// may inject ANSI sequences even in non-interactive mode.
	baseBranch = sanitizeBranch(baseBranch)
	if baseBranch == "" {
		// Try auto-detection from the current branch's upstream or merge-base.
		if detector != nil {
			if detected, err := detector.DetectBaseBranch(context.Background()); err == nil && detected != "" {
				baseBranch = detected
				fmt.Fprintf(os.Stderr, "  Auto-detected base branch: %s\n", baseBranch)
			}
		}
		// If auto-detect didn't yield a result, fall through to prompt.
		if baseBranch == "" {
			if prompter == nil {
				return Config{}, fmt.Errorf("--base-branch is required (auto-detection failed; or run interactively with a TTY)")
			}
			branch, err := prompter.ReadBaseBranch()
			if err != nil {
				return Config{}, fmt.Errorf("interactive prompt for base-branch: %w", err)
			}
			baseBranch = branch
		}
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

	// Resolve Postgres image: explicit flag > env > default.
	// The flag defaults to defaultPostgresImage, so DBFLOW_POSTGRES_IMAGE is only
	// consulted when the flag was left at its default (not explicitly provided).
	postgresImage = sanitizeImage(postgresImage)
	if !flagSet["postgres-image"] {
		if envImage := sanitizeImage(env(postgresImageEnvVar)); envImage != "" {
			postgresImage = envImage
		}
	}
	if postgresImage == "" {
		postgresImage = defaultPostgresImage
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
		PostgresImage: postgresImage,
	}, nil
}
