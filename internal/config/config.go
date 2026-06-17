// Package config resolves CLI flags and environment variables into a Config value.
// Precedence: flags > env > defaults.
// The git token is read exclusively from DBFLOW_GIT_TOKEN — never from a flag,
// never written to disk, and never emitted in logs or String() output.
package config

import (
	"flag"
	"fmt"
	"strings"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
)

const (
	tokenEnvVar    = "DBFLOW_GIT_TOKEN"
	defaultBranch  = "integracion"
	defaultFormat  = "console"
	defaultLogLvl  = "info"
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

// Resolve parses args using stdlib flag and looks up env variables via the
// provided env func. Returns an error for missing required inputs.
func Resolve(args []string, env func(string) string) (Config, error) {
	fs := flag.NewFlagSet("dbflow-validator", flag.ContinueOnError)

	var (
		repoURL      string
		baseBranch   string
		outputFormat string
		outputFile   string
		logLevel     string
	)

	fs.StringVar(&repoURL, "repo-url", "", "Repository URL to clone and validate (required)")
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

	if repoURL == "" {
		return Config{}, fmt.Errorf("--repo-url is required")
	}

	rawToken := env(tokenEnvVar)
	if rawToken == "" {
		return Config{}, fmt.Errorf("%s environment variable is required; set it to your git access token", tokenEnvVar)
	}

	return Config{
		RepoURL:      repoURL,
		BaseBranch:   baseBranch,
		OutputFormat: outputFormat,
		OutputFile:   outputFile,
		LogLevel:     logLevel,
		Token:        domain.NewSecret(rawToken),
	}, nil
}
