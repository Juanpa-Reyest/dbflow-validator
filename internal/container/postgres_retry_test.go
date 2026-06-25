package container_test

import (
	"context"
	"errors"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/container"
	"github.com/dbflow-validator/dbflow-validator/internal/domain"
)

// TestStartWithStarter_Retry tests the injectable seam for postgres container
// creation without requiring a live Docker daemon. The seam accepts a starter
// func that matches the signature of the real startOnce impl.

func TestStartWithStarter_Retry(t *testing.T) {
	t.Run("starter succeeds first attempt — returns valid coords", func(t *testing.T) {
		want := domain.ContainerCoords{
			Host: "localhost", Port: 5433,
			User: "validator", Password: "pass", DBName: "testdb",
		}
		starter := func(_ context.Context, _ string) (domain.ContainerCoords, error) {
			return want, nil
		}

		p := container.NewPostgresProvider()
		got, err := p.StartWithStarter(context.Background(), "net-name", starter, 0)
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		if got != want {
			t.Errorf("expected coords %+v, got %+v", want, got)
		}
	})

	t.Run("starter fails twice then succeeds — returns valid coords", func(t *testing.T) {
		want := domain.ContainerCoords{
			Host: "localhost", Port: 5434,
			User: "validator", Password: "pass", DBName: "testdb",
		}
		calls := 0
		starter := func(_ context.Context, _ string) (domain.ContainerCoords, error) {
			calls++
			if calls < 3 {
				return domain.ContainerCoords{}, errors.New("transient start error")
			}
			return want, nil
		}

		p := container.NewPostgresProvider()
		got, err := p.StartWithStarter(context.Background(), "net-name", starter, 0)
		if err != nil {
			t.Fatalf("expected no error after retry, got: %v", err)
		}
		if got != want {
			t.Errorf("expected coords %+v, got %+v", want, got)
		}
		if calls != 3 {
			t.Errorf("expected 3 starter calls, got %d", calls)
		}
	})

	t.Run("starter always fails — returns last error", func(t *testing.T) {
		sentinel := errors.New("persistent start failure")
		calls := 0
		starter := func(_ context.Context, _ string) (domain.ContainerCoords, error) {
			calls++
			return domain.ContainerCoords{}, sentinel
		}

		p := container.NewPostgresProvider()
		_, err := p.StartWithStarter(context.Background(), "net-name", starter, 0)
		if !errors.Is(err, sentinel) {
			t.Fatalf("expected sentinel error, got: %v", err)
		}
		// Default attempts is 3.
		if calls != 3 {
			t.Errorf("expected 3 starter calls, got %d", calls)
		}
	})

	t.Run("ctx cancelled before first attempt — returns ctx error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		calls := 0
		starter := func(_ context.Context, _ string) (domain.ContainerCoords, error) {
			calls++
			return domain.ContainerCoords{Host: "localhost", Port: 5432}, nil
		}

		p := container.NewPostgresProvider()
		_, err := p.StartWithStarter(ctx, "net-name", starter, 0)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got: %v", err)
		}
		if calls != 0 {
			t.Errorf("expected 0 starter calls on pre-cancelled ctx, got %d", calls)
		}
	})
}
