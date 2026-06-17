// Package container provides ephemeral PostgreSQL container lifecycle and readiness probing.
package container

import (
	"context"
	"fmt"
	"time"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
)

// PingFunc is a function that tests whether the database is accepting connections.
// It should return nil on success, or an error if not ready.
type PingFunc func(ctx context.Context) error

// RetryPolicy configures the bounded exponential-backoff retry loop used by WaitReady.
type RetryPolicy struct {
	// InitialInterval is the wait duration before the second attempt.
	InitialInterval time.Duration
	// Multiplier grows the interval on each failure (e.g. 1.5 for 50% growth).
	Multiplier float64
	// MaxInterval caps the growth so retries never wait longer than this.
	MaxInterval time.Duration
	// Deadline is the total wall-clock budget for the probe.
	Deadline time.Duration
}

// DefaultRetryPolicy is the policy used when no custom policy is provided.
var DefaultRetryPolicy = RetryPolicy{
	InitialInterval: 200 * time.Millisecond,
	Multiplier:      1.5,
	MaxInterval:     2 * time.Second,
	Deadline:        60 * time.Second,
}

// WaitReady probes the database using ping in a bounded retry loop.
// It is fully unit-testable via the injected now (clock) and sleep functions.
//
// Returns:
//   - nil when ping succeeds before the deadline.
//   - domain.ErrReadinessTimeout when the deadline is exhausted.
//   - ctx.Err() when the context is cancelled or times out.
func WaitReady(
	ctx context.Context,
	ping PingFunc,
	policy RetryPolicy,
	now func() time.Time,
	sleep func(time.Duration),
) error {
	deadline := now().Add(policy.Deadline)
	interval := policy.InitialInterval

	for {
		// Check context cancellation first.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Check wall-clock deadline.
		if now().After(deadline) {
			return fmt.Errorf("%w: exhausted after %s", domain.ErrReadinessTimeout, policy.Deadline)
		}

		// Attempt ping.
		if err := ping(ctx); err == nil {
			return nil
		}

		// Check context again after ping (ping might have taken a while).
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Sleep before next retry, respecting interval cap.
		sleep(interval)

		// Grow interval.
		next := time.Duration(float64(interval) * policy.Multiplier)
		if next > policy.MaxInterval {
			next = policy.MaxInterval
		}
		interval = next
	}
}
