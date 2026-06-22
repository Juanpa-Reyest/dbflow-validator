// Package container provides ephemeral container lifecycle management.
// This file owns the per-run Docker network used to connect Postgres and Maven containers.
package container

import (
	"context"
	"fmt"

	"github.com/testcontainers/testcontainers-go/network"
)

// NewNetwork creates a user-defined Docker bridge network for a single run.
//
// The network name is assigned by testcontainers-go (a generated UUID); the
// library exposes no customizer to override it. Orphaned networks are reaped
// automatically via testcontainers' Ryuk labels, so the name is used only for
// identification in logs and the run report.
//
// Returns:
//   - id:      the Docker network ID (long hex string)
//   - name:    the network name assigned by testcontainers (a UUID)
//   - cleanup: a function that removes the network; safe to call multiple times
//              (idempotent — a second call logs but does not panic)
//   - err:     non-nil if the network could not be created
//
// The caller MUST register cleanup in the CleanupRegistry BEFORE passing the
// network to any container, so LIFO teardown removes the network last.
func NewNetwork(ctx context.Context) (id, name string, cleanup func() error, err error) {
	net, err := network.New(ctx,
		network.WithDriver("bridge"),
	)
	if err != nil {
		return "", "", nil, fmt.Errorf("create docker network: %w", err)
	}

	// Use the Docker-assigned ID and name from the response.
	id = net.ID
	name = net.Name

	var removed bool
	cleanup = func() error {
		if removed {
			return nil
		}
		removed = true
		if removeErr := net.Remove(ctx); removeErr != nil {
			return fmt.Errorf("remove docker network %q: %w", name, removeErr)
		}
		return nil
	}

	return id, name, cleanup, nil
}
