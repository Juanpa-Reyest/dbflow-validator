// Package container provides ephemeral container lifecycle management.
// This file owns the generic bounded-retry helper used for transient Docker operations.
package container

import (
	"context"
	"time"
)

// RetryDo runs fn up to attempts times, sleeping backoff between failures.
//
// Semantics:
//   - If fn succeeds on any attempt, RetryDo returns nil immediately.
//   - If all attempts fail, RetryDo returns the error from the last attempt.
//   - If ctx is already done when RetryDo is called, it returns ctx.Err() without
//     calling fn.
//   - If ctx becomes done while sleeping between attempts, RetryDo returns ctx.Err()
//     without starting the next attempt.
//   - backoff == 0 skips the sleep entirely (useful in unit tests).
//   - attempts <= 0 is treated as 1 (at least one attempt is always made, unless
//     ctx is pre-cancelled).
func RetryDo(ctx context.Context, attempts int, backoff time.Duration, fn func() error) error {
	if attempts <= 0 {
		attempts = 1
	}

	var lastErr error
	for i := 0; i < attempts; i++ {
		// Respect ctx cancellation before each attempt.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := fn(); err == nil {
			return nil
		} else {
			lastErr = err
		}

		// Do not sleep after the final attempt.
		if i == attempts-1 {
			break
		}

		// Sleep between attempts, but bail out if ctx is cancelled during sleep.
		if backoff > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}
	}

	return lastErr
}
