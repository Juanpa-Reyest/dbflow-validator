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
	// Only docker and git are required; mvn and java must NOT be checked.
	allRequired := map[string]string{
		"docker": "/usr/bin/docker",
		"git":    "/usr/bin/git",
	}

	tests := []struct {
		name        string
		found       map[string]string
		wantErr     bool
		errContains string
		wantCount   int
	}{
		{
			name:      "docker and git present - passes (mvn/java absent is fine)",
			found:     allRequired,
			wantErr:   false,
			wantCount: 2,
		},
		{
			name: "mvn and java absent - still passes",
			found: map[string]string{
				"docker": "/usr/bin/docker",
				"git":    "/usr/bin/git",
				// mvn and java intentionally absent
			},
			wantErr:   false,
			wantCount: 2,
		},
		{
			name: "docker missing - error names docker",
			found: map[string]string{
				"git": "/usr/bin/git",
			},
			wantErr:     true,
			errContains: "docker",
		},
		{
			name: "git missing - error names git",
			found: map[string]string{
				"docker": "/usr/bin/docker",
			},
			wantErr:     true,
			errContains: "git",
		},
		{
			name:        "both docker and git missing - error mentions docker first",
			found:       map[string]string{},
			wantErr:     true,
			errContains: "docker",
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
			if err == nil && tt.wantCount > 0 && len(statuses) != tt.wantCount {
				t.Errorf("expected %d ToolStatus entries, got %d", tt.wantCount, len(statuses))
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
