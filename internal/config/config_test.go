package config_test

import (
	"strings"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/config"
	"github.com/dbflow-validator/dbflow-validator/internal/domain"
)

// mockPromptReader implements config.PromptReader for testing.
type mockPromptReader struct {
	url   string
	token string
	urlErr error
	tokenErr error
}

func (m *mockPromptReader) ReadRepoURL() (string, error) {
	return m.url, m.urlErr
}
func (m *mockPromptReader) ReadToken() (domain.Secret, error) {
	if m.tokenErr != nil {
		return domain.Secret{}, m.tokenErr
	}
	return domain.NewSecret(m.token), nil
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		env     map[string]string
		wantErr bool
		check   func(t *testing.T, cfg config.Config)
	}{
		{
			name: "all flags explicit with token from env",
			args: []string{
				"--repo-url", "https://host/repo.git",
				"--base-branch", "main",
				"--output-format", "json",
				"--output-file", "out.json",
				"--log-level", "debug",
			},
			env: map[string]string{
				"DBFLOW_GIT_TOKEN": "secret",
			},
			check: func(t *testing.T, cfg config.Config) {
				t.Helper()
				if cfg.RepoURL != "https://host/repo.git" {
					t.Errorf("RepoURL: got %q, want %q", cfg.RepoURL, "https://host/repo.git")
				}
				if cfg.BaseBranch != "main" {
					t.Errorf("BaseBranch: got %q, want %q", cfg.BaseBranch, "main")
				}
				if cfg.OutputFormat != "json" {
					t.Errorf("OutputFormat: got %q, want %q", cfg.OutputFormat, "json")
				}
				if cfg.OutputFile != "out.json" {
					t.Errorf("OutputFile: got %q, want %q", cfg.OutputFile, "out.json")
				}
				if cfg.LogLevel != "debug" {
					t.Errorf("LogLevel: got %q, want %q", cfg.LogLevel, "debug")
				}
				// Token value must not appear in any Config string representation.
				repr := cfg.String()
				if strings.Contains(repr, "secret") {
					t.Errorf("token value leaked in Config.String(): %q", repr)
				}
			},
		},
		{
			name:    "missing required --repo-url with no prompter returns error",
			args:    []string{},
			env:     map[string]string{"DBFLOW_GIT_TOKEN": "tok"},
			wantErr: true,
		},
		{
			name:    "missing DBFLOW_GIT_TOKEN with no prompter returns error",
			args:    []string{"--repo-url", "https://host/repo.git"},
			env:     map[string]string{},
			wantErr: true,
		},
		{
			name: "default base-branch is integracion",
			args: []string{"--repo-url", "https://host/repo.git"},
			env:  map[string]string{"DBFLOW_GIT_TOKEN": "tok"},
			check: func(t *testing.T, cfg config.Config) {
				t.Helper()
				if cfg.BaseBranch != "integracion" {
					t.Errorf("BaseBranch: got %q, want %q", cfg.BaseBranch, "integracion")
				}
			},
		},
		{
			name: "default output-format is console",
			args: []string{"--repo-url", "https://host/repo.git"},
			env:  map[string]string{"DBFLOW_GIT_TOKEN": "tok"},
			check: func(t *testing.T, cfg config.Config) {
				t.Helper()
				if cfg.OutputFormat != "console" {
					t.Errorf("OutputFormat: got %q, want %q", cfg.OutputFormat, "console")
				}
			},
		},
		{
			name: "default log-level is info",
			args: []string{"--repo-url", "https://host/repo.git"},
			env:  map[string]string{"DBFLOW_GIT_TOKEN": "tok"},
			check: func(t *testing.T, cfg config.Config) {
				t.Helper()
				if cfg.LogLevel != "info" {
					t.Errorf("LogLevel: got %q, want %q", cfg.LogLevel, "info")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := func(key string) string { return tt.env[key] }
			cfg, err := config.Resolve(tt.args, env)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Resolve() error = %v, wantErr = %v", err, tt.wantErr)
			}
			if err == nil && tt.check != nil {
				tt.check(t, cfg)
			}
		})
	}
}

// TestResolveWithPrompter verifies the interactive prompt path via injectable PromptReader.
func TestResolveWithPrompter(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		env         map[string]string
		prompter    *mockPromptReader
		wantErr     bool
		errContains string
		check       func(t *testing.T, cfg config.Config)
	}{
		{
			name:     "no repo-url and no token - prompter provides both",
			args:     []string{},
			env:      map[string]string{},
			prompter: &mockPromptReader{url: "https://prompt.repo/repo.git", token: "prompted-token"},
			check: func(t *testing.T, cfg config.Config) {
				t.Helper()
				if cfg.RepoURL != "https://prompt.repo/repo.git" {
					t.Errorf("RepoURL from prompt: got %q", cfg.RepoURL)
				}
				repr := cfg.String()
				if strings.Contains(repr, "prompted-token") {
					t.Errorf("token from prompt leaked in String(): %q", repr)
				}
			},
		},
		{
			name: "flag wins over prompt for repo-url (flag > prompt precedence)",
			args: []string{"--repo-url", "https://flag.repo/repo.git"},
			env:  map[string]string{},
			prompter: &mockPromptReader{
				url:   "https://prompt.repo/repo.git",
				token: "tok",
			},
			check: func(t *testing.T, cfg config.Config) {
				t.Helper()
				if cfg.RepoURL != "https://flag.repo/repo.git" {
					t.Errorf("RepoURL: flag should win, got %q", cfg.RepoURL)
				}
			},
		},
		{
			name: "env token wins over prompt token (env > prompt precedence)",
			args: []string{},
			env:  map[string]string{"DBFLOW_GIT_TOKEN": "env-token"},
			prompter: &mockPromptReader{
				url:   "https://prompt.repo/repo.git",
				token: "prompt-token",
			},
			check: func(t *testing.T, cfg config.Config) {
				t.Helper()
				// We can only verify the token is NOT in String() and that no error occurred.
				repr := cfg.String()
				if strings.Contains(repr, "env-token") || strings.Contains(repr, "prompt-token") {
					t.Errorf("token leaked in String(): %q", repr)
				}
			},
		},
		{
			name:        "no repo-url and no prompter - returns error (non-TTY path)",
			args:        []string{},
			env:         map[string]string{"DBFLOW_GIT_TOKEN": "tok"},
			prompter:    nil,
			wantErr:     true,
			errContains: "repo-url",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := func(key string) string { return tt.env[key] }
			var prompter config.PromptReader
			if tt.prompter != nil {
				prompter = tt.prompter
			}
			cfg, err := config.ResolveWithPrompter(tt.args, env, prompter)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ResolveWithPrompter() error = %v, wantErr = %v", err, tt.wantErr)
			}
			if err != nil && tt.errContains != "" {
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errContains)
				}
			}
			if err == nil && tt.check != nil {
				tt.check(t, cfg)
			}
		})
	}
}
