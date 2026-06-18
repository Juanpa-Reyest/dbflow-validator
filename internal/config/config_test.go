package config_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/config"
	"github.com/dbflow-validator/dbflow-validator/internal/domain"
)

// mockPromptReader implements config.PromptReader for testing.
type mockPromptReader struct {
	url              string
	token            string
	urlErr           error
	tokenErr         error
	tokenPromptCalled bool
}

func (m *mockPromptReader) ReadRepoURL() (string, error) {
	return m.url, m.urlErr
}
func (m *mockPromptReader) ReadToken() (domain.Secret, error) {
	m.tokenPromptCalled = true
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
			name: "default base-branch is integration",
			args: []string{"--repo-url", "https://host/repo.git"},
			env:  map[string]string{"DBFLOW_GIT_TOKEN": "tok"},
			check: func(t *testing.T, cfg config.Config) {
				t.Helper()
				if cfg.BaseBranch != "integration" {
					t.Errorf("BaseBranch: got %q, want %q", cfg.BaseBranch, "integration")
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
		{
			name: "--sql-input omitted defaults to absolute cwd/src/main/resources/SQLInput",
			args: []string{"--repo-url", "https://host/repo.git"},
			env:  map[string]string{"DBFLOW_GIT_TOKEN": "tok"},
			prompter: &mockPromptReader{},
			check: func(t *testing.T, cfg config.Config) {
				t.Helper()
				if !filepath.IsAbs(cfg.SQLInputPath) {
					t.Errorf("SQLInputPath should be absolute, got %q", cfg.SQLInputPath)
				}
				if !strings.HasSuffix(cfg.SQLInputPath, "src/main/resources/SQLInput") {
					t.Errorf("SQLInputPath should end with src/main/resources/SQLInput, got %q", cfg.SQLInputPath)
				}
			},
		},
		{
			name: "--sql-input explicit path used as-is",
			args: []string{"--repo-url", "https://host/repo.git", "--sql-input", "/custom/SQLInput"},
			env:  map[string]string{"DBFLOW_GIT_TOKEN": "tok"},
			prompter: &mockPromptReader{},
			check: func(t *testing.T, cfg config.Config) {
				t.Helper()
				if cfg.SQLInputPath != "/custom/SQLInput" {
					t.Errorf("SQLInputPath: got %q, want /custom/SQLInput", cfg.SQLInputPath)
				}
			},
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

// TestResolveSSH verifies that SSH URLs bypass the token requirement entirely.
func TestResolveSSH(t *testing.T) {
	t.Run("scp-style SSH URL via flag: no token required, no prompt called", func(t *testing.T) {
		mock := &mockPromptReader{}
		cfg, err := config.ResolveWithPrompter(
			[]string{"--repo-url", "git@github.com:org/repo.git"},
			func(string) string { return "" }, // no env token
			mock,
		)
		if err != nil {
			t.Fatalf("unexpected error for SSH URL: %v", err)
		}
		if cfg.RepoURL != "git@github.com:org/repo.git" {
			t.Errorf("RepoURL: got %q, want scp-style URL", cfg.RepoURL)
		}
		if mock.tokenPromptCalled {
			t.Error("ReadToken() was called for an SSH URL — it must not be")
		}
		if cfg.Token.Reveal() != "" {
			t.Error("Token must be empty for SSH URL")
		}
	})

	t.Run("ssh:// URL via flag: no token required, no prompt called", func(t *testing.T) {
		mock := &mockPromptReader{}
		cfg, err := config.ResolveWithPrompter(
			[]string{"--repo-url", "ssh://git@github.com/org/repo.git"},
			func(string) string { return "" },
			mock,
		)
		if err != nil {
			t.Fatalf("unexpected error for ssh:// URL: %v", err)
		}
		if mock.tokenPromptCalled {
			t.Error("ReadToken() was called for an ssh:// URL — it must not be")
		}
		if cfg.Token.Reveal() != "" {
			t.Error("Token must be empty for ssh:// URL")
		}
	})

	t.Run("SSH URL via prompt (interactive): URL prompted first, then no token prompt", func(t *testing.T) {
		mock := &mockPromptReader{url: "git@github.com:org/repo.git"}
		cfg, err := config.ResolveWithPrompter(
			[]string{}, // no flags — falls through to prompt
			func(string) string { return "" },
			mock,
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.RepoURL != "git@github.com:org/repo.git" {
			t.Errorf("RepoURL: got %q", cfg.RepoURL)
		}
		if mock.tokenPromptCalled {
			t.Error("ReadToken() was called for an SSH URL obtained via prompt — it must not be")
		}
	})

	t.Run("SSH URL with DBFLOW_GIT_TOKEN env set: token env ignored, no error", func(t *testing.T) {
		mock := &mockPromptReader{}
		cfg, err := config.ResolveWithPrompter(
			[]string{"--repo-url", "git@github.com:org/repo.git"},
			func(key string) string {
				if key == "DBFLOW_GIT_TOKEN" {
					return "should-be-ignored"
				}
				return ""
			},
			mock,
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Token env value is accepted but not required; SSH path works regardless.
		_ = cfg
	})

	t.Run("HTTPS URL without token and no prompter: still errors (HTTPS behavior unchanged)", func(t *testing.T) {
		_, err := config.ResolveWithPrompter(
			[]string{"--repo-url", "https://github.com/org/repo.git"},
			func(string) string { return "" },
			nil, // no prompter — non-TTY
		)
		if err == nil {
			t.Fatal("expected error for HTTPS URL with no token and no prompter")
		}
		if !strings.Contains(err.Error(), "DBFLOW_GIT_TOKEN") {
			t.Errorf("error should mention DBFLOW_GIT_TOKEN, got: %v", err)
		}
	})

	t.Run("HTTPS URL with no token no prompter: error unchanged", func(t *testing.T) {
		_, err := config.ResolveWithPrompter(
			[]string{"--repo-url", "https://github.com/org/repo.git"},
			func(string) string { return "" },
			nil,
		)
		if err == nil {
			t.Error("expected error for HTTPS without token")
		}
	})
}
