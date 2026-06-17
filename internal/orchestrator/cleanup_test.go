package orchestrator_test

import (
	"errors"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/orchestrator"
)

func TestCleanupRegistry(t *testing.T) {
	t.Run("LIFO order", func(t *testing.T) {
		var order []int
		reg := orchestrator.NewCleanupRegistry()
		reg.Register(func() error { order = append(order, 1); return nil })
		reg.Register(func() error { order = append(order, 2); return nil })
		reg.Register(func() error { order = append(order, 3); return nil })

		errs := reg.RunAll()
		if len(errs) != 0 {
			t.Fatalf("expected no errors, got %v", errs)
		}
		if len(order) != 3 || order[0] != 3 || order[1] != 2 || order[2] != 1 {
			t.Errorf("expected LIFO [3 2 1], got %v", order)
		}
	})

	t.Run("run-once: second RunAll is a no-op", func(t *testing.T) {
		calls := 0
		reg := orchestrator.NewCleanupRegistry()
		reg.Register(func() error { calls++; return nil })

		reg.RunAll()
		reg.RunAll() // second call must be idempotent

		if calls != 1 {
			t.Errorf("expected cleaner to run exactly once, ran %d times", calls)
		}
	})

	t.Run("error in one cleaner does not skip subsequent cleaners", func(t *testing.T) {
		var ran []string
		reg := orchestrator.NewCleanupRegistry()
		reg.Register(func() error { ran = append(ran, "first"); return nil })
		reg.Register(func() error { ran = append(ran, "second"); return errors.New("boom") })
		reg.Register(func() error { ran = append(ran, "third"); return nil })

		errs := reg.RunAll()
		// LIFO: third → second → first
		if len(ran) != 3 {
			t.Errorf("expected 3 cleaners to run, got %d: %v", len(ran), ran)
		}
		if len(errs) != 1 {
			t.Errorf("expected 1 error, got %d: %v", len(errs), errs)
		}
	})

	t.Run("RunAll on empty registry is safe", func(t *testing.T) {
		reg := orchestrator.NewCleanupRegistry()
		errs := reg.RunAll()
		if errs != nil && len(errs) != 0 {
			t.Errorf("expected no errors on empty registry, got %v", errs)
		}
	})
}
