package container_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dbflow-validator/dbflow-validator/internal/container"
)

// TestRetryDo covers the three required behaviours of the bounded retry helper.

func TestRetryDo(t *testing.T) {
	t.Run("fn fails N-1 times then succeeds — returns nil", func(t *testing.T) {
		const attempts = 3
		calls := 0
		fn := func() error {
			calls++
			if calls < attempts {
				return errors.New("transient")
			}
			return nil
		}

		err := container.RetryDo(context.Background(), attempts, 0, fn)
		if err != nil {
			t.Fatalf("expected nil error after %d attempts, got: %v", attempts, err)
		}
		if calls != attempts {
			t.Errorf("expected fn called %d times, got %d", attempts, calls)
		}
	})

	t.Run("fn always fails — returns last error after all attempts", func(t *testing.T) {
		const maxAttempts = 4
		sentinel := errors.New("persistent failure")
		calls := 0
		fn := func() error {
			calls++
			return sentinel
		}

		err := container.RetryDo(context.Background(), maxAttempts, 0, fn)
		if !errors.Is(err, sentinel) {
			t.Fatalf("expected sentinel error, got: %v", err)
		}
		if calls != maxAttempts {
			t.Errorf("expected fn called %d times, got %d", maxAttempts, calls)
		}
	})

	t.Run("ctx cancelled before first attempt — returns ctx err immediately", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // already cancelled

		calls := 0
		fn := func() error {
			calls++
			return nil
		}

		err := container.RetryDo(ctx, 5, 0, fn)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got: %v", err)
		}
		// fn should not have been called when ctx is already done.
		if calls != 0 {
			t.Errorf("expected fn not called when ctx pre-cancelled, got %d calls", calls)
		}
	})

	t.Run("ctx cancelled mid-retry — stops promptly", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		calls := 0
		fn := func() error {
			calls++
			if calls == 2 {
				cancel()
			}
			return errors.New("transient")
		}

		// Use a non-zero backoff to make the sleep meaningful; the ctx should
		// cut the sleep short.
		err := container.RetryDo(ctx, 10, 10*time.Millisecond, fn)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got: %v", err)
		}
		// fn was called at most 3 times (attempt 1, attempt 2 cancels, attempt 3 may or may not run
		// depending on select ordering) — definitely not all 10.
		if calls > 3 {
			t.Errorf("expected at most 3 fn calls after cancellation, got %d", calls)
		}
	})

	t.Run("single attempt — no retry on success", func(t *testing.T) {
		calls := 0
		fn := func() error {
			calls++
			return nil
		}

		err := container.RetryDo(context.Background(), 1, 0, fn)
		if err != nil {
			t.Fatalf("expected nil error, got: %v", err)
		}
		if calls != 1 {
			t.Errorf("expected 1 call, got %d", calls)
		}
	})

	t.Run("single attempt — failure returned immediately", func(t *testing.T) {
		sentinel := errors.New("only attempt failed")
		err := container.RetryDo(context.Background(), 1, 0, func() error {
			return sentinel
		})
		if !errors.Is(err, sentinel) {
			t.Fatalf("expected sentinel error, got: %v", err)
		}
	})
}
