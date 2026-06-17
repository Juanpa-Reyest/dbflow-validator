package preflight_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/preflight"
)

// TestPreflight_DaemonProbe exercises the three daemon-probe scenarios using
// table-driven tests and injectable fakes — no real Docker daemon required.
func TestPreflight_DaemonProbe(t *testing.T) {
	allBinaries := map[string]string{
		"docker": "/usr/bin/docker",
		"mvn":    "/usr/bin/mvn",
		"git":    "/usr/bin/git",
		"java":   "/usr/bin/java",
	}
	withoutDocker := map[string]string{
		"mvn":  "/usr/bin/mvn",
		"git":  "/usr/bin/git",
		"java": "/usr/bin/java",
	}

	daemonUp := func(_ context.Context) error { return nil }
	daemonDown := func(_ context.Context) error {
		return errors.New("cannot connect to Docker daemon")
	}

	tests := []struct {
		name          string
		lookPathFound map[string]string
		daemonProber  func(context.Context) error
		wantErr       bool
		errContains   string
		// notContains ensures the message is distinct from the "not found" case.
		notContains string
	}{
		{
			name:          "binary missing - daemon not probed - distinct error",
			lookPathFound: withoutDocker,
			daemonProber:  daemonUp, // irrelevant - should never be called
			wantErr:       true,
			errContains:   "docker",
			notContains:   "daemon",
		},
		{
			name:          "binary present and daemon up - no error",
			lookPathFound: allBinaries,
			daemonProber:  daemonUp,
			wantErr:       false,
		},
		{
			name:          "binary present but daemon down - friendly daemon error",
			lookPathFound: allBinaries,
			daemonProber:  daemonDown,
			wantErr:       true,
			errContains:   "daemon",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := preflight.NewWithDaemonProber(
				makeFakeLookPath(tt.lookPathFound),
				tt.daemonProber,
			)
			_, err := p.Check(context.Background())

			if (err != nil) != tt.wantErr {
				t.Fatalf("Check() error = %v, wantErr = %v", err, tt.wantErr)
			}
			if err != nil && tt.errContains != "" {
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error %q does not mention %q", err.Error(), tt.errContains)
				}
			}
			if err != nil && tt.notContains != "" {
				if strings.Contains(err.Error(), tt.notContains) {
					t.Errorf("error %q should NOT mention %q (must be distinct from daemon error)",
						err.Error(), tt.notContains)
				}
			}
		})
	}
}
