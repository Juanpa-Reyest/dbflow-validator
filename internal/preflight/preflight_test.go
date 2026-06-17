package preflight_test

import (
	"context"
	"errors"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/preflight"
)

// fakeLookPath simulates exec.LookPath with a whitelist of found binaries.
func makeFakeLookPath(found map[string]string) func(string) (string, error) {
	return func(name string) (string, error) {
		if path, ok := found[name]; ok {
			return path, nil
		}
		return "", errors.New(name + ": not found")
	}
}

func TestPreflight_Check(t *testing.T) {
	allFound := map[string]string{
		"docker": "/usr/bin/docker",
		"mvn":    "/usr/bin/mvn",
		"git":    "/usr/bin/git",
		"java":   "/usr/bin/java",
	}

	tests := []struct {
		name        string
		found       map[string]string
		wantErr     bool
		errContains string
	}{
		{
			name:    "all four binaries found - no error",
			found:   allFound,
			wantErr: false,
		},
		{
			name: "docker missing - error names docker",
			found: map[string]string{
				"mvn":  "/usr/bin/mvn",
				"git":  "/usr/bin/git",
				"java": "/usr/bin/java",
			},
			wantErr:     true,
			errContains: "docker",
		},
		{
			name: "mvn missing - error names mvn",
			found: map[string]string{
				"docker": "/usr/bin/docker",
				"git":    "/usr/bin/git",
				"java":   "/usr/bin/java",
			},
			wantErr:     true,
			errContains: "mvn",
		},
		{
			name: "git missing - error names git",
			found: map[string]string{
				"docker": "/usr/bin/docker",
				"mvn":    "/usr/bin/mvn",
				"java":   "/usr/bin/java",
			},
			wantErr:     true,
			errContains: "git",
		},
		{
			name: "java missing - error names java",
			found: map[string]string{
				"docker": "/usr/bin/docker",
				"mvn":    "/usr/bin/mvn",
				"git":    "/usr/bin/git",
			},
			wantErr:     true,
			errContains: "java",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := preflight.New(makeFakeLookPath(tt.found))
			statuses, err := p.Check(context.Background())

			if (err != nil) != tt.wantErr {
				t.Fatalf("Check() error = %v, wantErr = %v", err, tt.wantErr)
			}
			if err != nil && tt.errContains != "" {
				if !containsStr(err.Error(), tt.errContains) {
					t.Errorf("error %q does not mention %q", err.Error(), tt.errContains)
				}
			}
			if err == nil && len(statuses) != 4 {
				t.Errorf("expected 4 ToolStatus entries, got %d", len(statuses))
			}
		})
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
