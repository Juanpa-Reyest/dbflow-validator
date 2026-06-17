// Package preflight verifies that required host binaries are on the PATH
// and that the Docker daemon is reachable before any external operation
// (clone, container start, Maven) is attempted.
package preflight

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
)

// execLookPath is the default production LookPath using os/exec.
var execLookPath = exec.LookPath

// requiredTools lists the binaries that must exist on the host PATH.
// Maven (mvn) and a JVM (java) are NOT required — they run inside a Docker container.
var requiredTools = []string{"docker", "git"}

// defaultDaemonProbeTimeout is the maximum time allowed for the Docker daemon ping.
const defaultDaemonProbeTimeout = 5 * time.Second

// defaultDaemonProber runs "docker version --format {{.Server.Version}}" with a
// short timeout to confirm the daemon is reachable.  It is the production prober.
func defaultDaemonProber(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, defaultDaemonProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Docker is installed but the daemon is not running — start Docker and retry: %w", err)
	}
	return nil
}

// Preflight checks host binary availability and Docker daemon liveness
// using injectable functions so the logic can be unit-tested without
// a real daemon or real binaries.
type Preflight struct {
	lookPath     func(string) (string, error)
	daemonProber func(context.Context) error
}

// New returns a Preflight that uses the standard exec.LookPath and a real
// "docker version" daemon probe with a 5-second timeout.
// Pass nil to use the default for either parameter.
func New(lookPath func(string) (string, error)) *Preflight {
	return NewWithDaemonProber(lookPath, nil)
}

// NewWithDaemonProber returns a Preflight with explicit injectable lookPath
// and daemonProber functions.  Pass nil for either to use the production default.
//
//   - lookPath — called once per required binary to confirm it is on the PATH.
//   - daemonProber — called after the Docker binary is found to confirm the daemon
//     is actually running.  Returning a non-nil error fails preflight with the
//     error message intact (so callers see a distinct "daemon not running" message
//     rather than a generic "not found" message).
func NewWithDaemonProber(
	lookPath func(string) (string, error),
	daemonProber func(context.Context) error,
) *Preflight {
	if lookPath == nil {
		lookPath = execLookPath
	}
	if daemonProber == nil {
		daemonProber = defaultDaemonProber
	}
	return &Preflight{
		lookPath:     lookPath,
		daemonProber: daemonProber,
	}
}

// Check verifies all required host tools are present and that the Docker
// daemon is reachable.  Returns (statuses, nil) when all checks pass, or
// (nil, error) on the first failure with a distinct, actionable message:
//
//   - Binary missing:  "... <name> not found on PATH — install it and re-run"
//   - Daemon down:     "Docker is installed but the daemon is not running — start Docker and retry"
func (p *Preflight) Check(ctx context.Context) ([]domain.ToolStatus, error) {
	statuses := make([]domain.ToolStatus, 0, len(requiredTools))

	for _, name := range requiredTools {
		path, err := p.lookPath(name)
		if err != nil {
			return nil, fmt.Errorf("%w: %q not found on PATH — install it and re-run: %v",
				domain.ErrPreflight, name, err)
		}
		statuses = append(statuses, domain.ToolStatus{
			Name:  name,
			Found: true,
			Path:  path,
		})

		// After finding the docker binary, confirm the daemon is actually running.
		if name == "docker" {
			if err := p.daemonProber(ctx); err != nil {
				return nil, fmt.Errorf("%w: %v", domain.ErrPreflight, err)
			}
		}
	}

	return statuses, nil
}
