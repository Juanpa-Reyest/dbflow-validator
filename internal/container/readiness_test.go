package container_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dbflow-validator/dbflow-validator/internal/container"
	"github.com/dbflow-validator/dbflow-validator/internal/domain"
)

func TestWaitReady(t *testing.T) {
	t.Run("succeeds on 3rd attempt", func(t *testing.T) {
		calls := 0
		ping := func(_ context.Context) error {
			calls++
			if calls < 3 {
				return errors.New("not ready yet")
			}
			return nil
		}

		// Fake clock: always return the same instant so no timeout fires.
		now := time.Now()
		fakeClock := func() time.Time { return now }
		fakeSleep := func(d time.Duration) {}

		policy := container.RetryPolicy{
			InitialInterval: 10 * time.Millisecond,
			Multiplier:      1.5,
			MaxInterval:     20 * time.Millisecond,
			Deadline:        5 * time.Second,
		}

		ctx := context.Background()
		attempts, err := container.WaitReady(ctx, ping, policy, fakeClock, fakeSleep)
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if calls != 3 {
			t.Fatalf("expected 3 ping calls, got %d", calls)
		}
		// AC-9: attempt count must match the number of ping calls.
		if attempts != 3 {
			t.Errorf("expected attempts=3, got %d", attempts)
		}
	})

	t.Run("succeeds on first attempt returns attempts=1", func(t *testing.T) {
		ping := func(_ context.Context) error { return nil }

		now := time.Now()
		fakeClock := func() time.Time { return now }
		fakeSleep := func(d time.Duration) {}

		policy := container.RetryPolicy{
			InitialInterval: 10 * time.Millisecond,
			Multiplier:      1.5,
			MaxInterval:     100 * time.Millisecond,
			Deadline:        5 * time.Second,
		}

		ctx := context.Background()
		attempts, err := container.WaitReady(ctx, ping, policy, fakeClock, fakeSleep)
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if attempts != 1 {
			t.Errorf("expected attempts=1, got %d", attempts)
		}
	})

	t.Run("deadline exceeded returns ErrReadinessTimeout", func(t *testing.T) {
		const lastErrMsg = "still not ready"
		ping := func(_ context.Context) error {
			return errors.New(lastErrMsg)
		}

		// Fake clock that advances by 1 second each call — will quickly exhaust a 2s deadline.
		callCount := 0
		base := time.Now()
		fakeClock := func() time.Time {
			callCount++
			return base.Add(time.Duration(callCount) * time.Second)
		}
		fakeSleep := func(d time.Duration) {}

		policy := container.RetryPolicy{
			InitialInterval: 10 * time.Millisecond,
			Multiplier:      1.5,
			MaxInterval:     100 * time.Millisecond,
			Deadline:        2 * time.Second,
		}

		ctx := context.Background()
		attempts, err := container.WaitReady(ctx, ping, policy, fakeClock, fakeSleep)
		if !errors.Is(err, domain.ErrReadinessTimeout) {
			t.Fatalf("expected ErrReadinessTimeout, got %v", err)
		}
		// AC-10: real driver error must be included in the returned error string.
		if !containsErrStr(err, lastErrMsg) {
			t.Errorf("expected last error %q to appear in error message; got: %v", lastErrMsg, err)
		}
		// AC-9: attempts must be > 0.
		if attempts == 0 {
			t.Errorf("expected attempts > 0 on timeout, got %d", attempts)
		}
	})

	t.Run("ctx cancelled mid-retry returns ctx.Err", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		calls := 0
		ping := func(_ context.Context) error {
			calls++
			if calls == 2 {
				cancel() // cancel on the 2nd attempt
			}
			return errors.New("not ready")
		}

		now := time.Now()
		fakeClock := func() time.Time { return now }
		fakeSleep := func(d time.Duration) {}

		policy := container.RetryPolicy{
			InitialInterval: 10 * time.Millisecond,
			Multiplier:      1.5,
			MaxInterval:     100 * time.Millisecond,
			Deadline:        30 * time.Second,
		}

		attempts, err := container.WaitReady(ctx, ping, policy, fakeClock, fakeSleep)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
		if attempts == 0 {
			t.Error("expected attempts > 0 when context is cancelled mid-retry")
		}
	})
}

// containsErrStr checks whether the error message of err contains substr.
func containsErrStr(err error, substr string) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, errors.New(substr)) || (len(err.Error()) > 0 && errContainsStr(err.Error(), substr))
}

func errContainsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
