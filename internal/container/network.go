// Package container provides ephemeral container lifecycle management.
// This file owns the per-run Docker network used to connect Postgres and Maven containers.
package container

import (
	"context"
	"fmt"
	"math/rand"

	"github.com/testcontainers/testcontainers-go/network"
)

const (
	networkNamePrefix = "dbflow-net-"
	networkSuffixLen  = 6
	networkAlphabet   = "abcdefghijklmnopqrstuvwxyz0123456789"
)

// NewNetwork creates a user-defined Docker bridge network with a randomised name
// following the pattern "dbflow-net-<6-char-rand>".
//
// Returns:
//   - id:      the Docker network ID (long hex string)
//   - name:    the human-readable network name (dbflow-net-xxxxxx)
//   - cleanup: a function that removes the network; safe to call multiple times
//              (idempotent — a second call logs but does not panic)
//   - err:     non-nil if the network could not be created
//
// The caller MUST register cleanup in the CleanupRegistry BEFORE passing the
// network to any container, so LIFO teardown removes the network last.
func NewNetwork(ctx context.Context) (id, name string, cleanup func() error, err error) {
	name = networkNamePrefix + randSuffix(networkSuffixLen)

	net, err := network.New(ctx,
		network.WithDriver("bridge"),
	)
	if err != nil {
		return "", "", nil, fmt.Errorf("create docker network %q: %w", name, err)
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

// randSuffix generates a random lowercase alphanumeric string of length n.
func randSuffix(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = networkAlphabet[rand.Intn(len(networkAlphabet))]
	}
	return string(b)
}
