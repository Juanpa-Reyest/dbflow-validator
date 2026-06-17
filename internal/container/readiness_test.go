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
		err := container.WaitReady(ctx, ping, policy, fakeClock, fakeSleep)
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if calls != 3 {
			t.Fatalf("expected 3 ping calls, got %d", calls)
		}
	})

	t.Run("deadline exceeded returns ErrReadinessTimeout", func(t *testing.T) {
		ping := func(_ context.Context) error {
			return errors.New("still not ready")
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
		err := container.WaitReady(ctx, ping, policy, fakeClock, fakeSleep)
		if !errors.Is(err, domain.ErrReadinessTimeout) {
			t.Fatalf("expected ErrReadinessTimeout, got %v", err)
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

		err := container.WaitReady(ctx, ping, policy, fakeClock, fakeSleep)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	})
}
