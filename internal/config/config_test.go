package config_test

import (
	"strings"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/config"
)

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
			name:    "missing required --repo-url returns error",
			args:    []string{},
			env:     map[string]string{"DBFLOW_GIT_TOKEN": "tok"},
			wantErr: true,
		},
		{
			name:    "missing DBFLOW_GIT_TOKEN returns error",
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
