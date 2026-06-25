package container_test

import (
	"context"
	"errors"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/container"
)

// TestNewNetworkWithCreator_Retry tests the injectable seam for network creation
// without requiring a live Docker daemon. The seam accepts a creator func that
// matches the signature of the real testcontainers-go network.New call.

func TestNewNetworkWithCreator_Retry(t *testing.T) {
	t.Run("creator succeeds first attempt — returns valid coords", func(t *testing.T) {
		creator := func(_ context.Context) (string, string, func() error, error) {
			return "net-id-1", "net-name-1", func() error { return nil }, nil
		}

		id, name, cleanup, err := container.NewNetworkWithCreator(context.Background(), creator, 0)
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		if id != "net-id-1" {
			t.Errorf("expected id %q, got %q", "net-id-1", id)
		}
		if name != "net-name-1" {
			t.Errorf("expected name %q, got %q", "net-name-1", name)
		}
		if cleanup == nil {
			t.Fatal("expected non-nil cleanup")
		}
	})

	t.Run("creator fails twice then succeeds — returns valid coords", func(t *testing.T) {
		calls := 0
		creator := func(_ context.Context) (string, string, func() error, error) {
			calls++
			if calls < 3 {
				return "", "", nil, errors.New("transient 500")
			}
			return "net-id-ok", "net-name-ok", func() error { return nil }, nil
		}

		id, name, cleanup, err := container.NewNetworkWithCreator(context.Background(), creator, 0)
		if err != nil {
			t.Fatalf("expected no error after retry, got: %v", err)
		}
		if id != "net-id-ok" {
			t.Errorf("expected id %q, got %q", "net-id-ok", id)
		}
		if name != "net-name-ok" {
			t.Errorf("expected name %q, got %q", "net-name-ok", name)
		}
		if cleanup == nil {
			t.Fatal("expected non-nil cleanup")
		}
		if calls != 3 {
			t.Errorf("expected 3 creator calls, got %d", calls)
		}
	})

	t.Run("creator always fails — returns last error", func(t *testing.T) {
		sentinel := errors.New("persistent 500")
		calls := 0
		creator := func(_ context.Context) (string, string, func() error, error) {
			calls++
			return "", "", nil, sentinel
		}

		_, _, _, err := container.NewNetworkWithCreator(context.Background(), creator, 0)
		if !errors.Is(err, sentinel) {
			t.Fatalf("expected sentinel error wrapped in create docker network error, got: %v", err)
		}
		// Should have been called exactly 3 times (default retry count for network).
		if calls != 3 {
			t.Errorf("expected 3 creator calls (default retry), got %d", calls)
		}
	})

	t.Run("ctx cancelled before first attempt — returns ctx error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		calls := 0
		creator := func(_ context.Context) (string, string, func() error, error) {
			calls++
			return "id", "name", func() error { return nil }, nil
		}

		_, _, _, err := container.NewNetworkWithCreator(ctx, creator, 0)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got: %v", err)
		}
		if calls != 0 {
			t.Errorf("expected 0 creator calls on pre-cancelled ctx, got %d", calls)
		}
	})
}
