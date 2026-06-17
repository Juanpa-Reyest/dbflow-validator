package container_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/container"
)

// TestNewNetwork_UnitValidation verifies the contract of NewNetwork using live Docker.
// It is an integration test guarded by testing.Short() because it requires a running
// Docker daemon to create and remove a real network.
func TestNewNetwork_UnitValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network integration test in -short mode (requires Docker daemon)")
	}

	ctx := context.Background()
	id, name, cleanup, err := container.NewNetwork(ctx)
	if err != nil {
		t.Fatalf("NewNetwork: %v", err)
	}

	// Verify non-empty ID and expected name pattern.
	if id == "" {
		t.Error("NewNetwork returned empty networkID")
	}
	if !strings.HasPrefix(name, "dbflow-net-") {
		t.Errorf("network name %q does not match pattern dbflow-net-<rand>", name)
	}
	if len(name) < len("dbflow-net-")+1 {
		t.Errorf("network name %q too short (expected random suffix)", name)
	}

	// cleanup must be a non-nil function.
	if cleanup == nil {
		t.Fatal("NewNetwork returned nil cleanup func")
	}

	// Cleanup must not error (network should still exist).
	if err := cleanup(); err != nil {
		t.Errorf("cleanup func returned error: %v", err)
	}

	// Second cleanup call must be idempotent (no panic, may error on already-gone network — acceptable).
	_ = cleanup()
}

// TestNewNetwork_NamePattern verifies that two consecutive networks get distinct names.
func TestNewNetwork_NamePattern(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network integration test in -short mode (requires Docker daemon)")
	}

	ctx := context.Background()
	_, name1, cleanup1, err := container.NewNetwork(ctx)
	if err != nil {
		t.Fatalf("NewNetwork 1: %v", err)
	}
	defer cleanup1() //nolint:errcheck

	_, name2, cleanup2, err := container.NewNetwork(ctx)
	if err != nil {
		t.Fatalf("NewNetwork 2: %v", err)
	}
	defer cleanup2() //nolint:errcheck

	if name1 == name2 {
		t.Errorf("two NewNetwork calls returned identical names %q — suffix is not random", name1)
	}
}
