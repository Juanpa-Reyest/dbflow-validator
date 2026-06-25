// Package container provides ephemeral container lifecycle management.
// This file owns the per-run Docker network used to connect Postgres and Maven containers.
package container

import (
	"context"
	"fmt"
	"time"

	"github.com/testcontainers/testcontainers-go/network"
)

// networkCreateAttempts is the number of times NewNetwork will retry a transient
// Docker failure (e.g. "500 Internal Server Error /networks/create" on Docker Desktop).
const networkCreateAttempts = 3

// networkCreateBackoff is the sleep between network-create retry attempts.
const networkCreateBackoff = time.Second

// networkCreatorFn is the injectable seam type for network creation.
// It matches the behaviour of defaultNetworkCreator and is used in tests.
type networkCreatorFn func(ctx context.Context) (id, name string, cleanup func() error, err error)

// defaultNetworkCreator is the real implementation used in production.
func defaultNetworkCreator(ctx context.Context) (id, name string, cleanup func() error, err error) {
	net, err := network.New(ctx,
		network.WithDriver("bridge"),
	)
	if err != nil {
		return "", "", nil, fmt.Errorf("create docker network: %w", err)
	}

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

// NewNetworkWithCreator creates a Docker bridge network, retrying up to
// networkCreateAttempts times on transient errors.
//
// The creator parameter is the injectable seam used in unit tests. Production
// callers must pass defaultNetworkCreator (or call NewNetwork which does so).
// The backoff parameter controls the sleep between retries; production code
// passes networkCreateBackoff and tests pass 0 for fast execution.
func NewNetworkWithCreator(ctx context.Context, creator networkCreatorFn, backoff time.Duration) (id, name string, cleanup func() error, err error) {
	var retID, retName string
	var retCleanup func() error

	retErr := RetryDo(ctx, networkCreateAttempts, backoff, func() error {
		var e error
		retID, retName, retCleanup, e = creator(ctx)
		return e
	})
	if retErr != nil {
		return "", "", nil, retErr
	}

	return retID, retName, retCleanup, nil
}

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
//
// Transient Docker failures (e.g. "500 Internal Server Error /networks/create")
// are retried up to networkCreateAttempts times with networkCreateBackoff between
// attempts, so intermittent Docker Desktop errors self-heal without user intervention.
func NewNetwork(ctx context.Context) (id, name string, cleanup func() error, err error) {
	return NewNetworkWithCreator(ctx, defaultNetworkCreator, networkCreateBackoff)
}
