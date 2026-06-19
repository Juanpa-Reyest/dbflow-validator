package orchestrator_test

// lazy_network_test.go — TDD tests for lazy Docker network creation (lazy-network change).
//
// Goal: the Docker network is created ONLY when the flow reaches container-start.
// Early failures (preflight, clone, engine-guard, overlay) must NEVER call the factory.
// On a full run: network is created, used by postgres + maven, torn down last (LIFO).
// On factory error at container-start: clean failure + proper teardown of prior resources.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
	"github.com/dbflow-validator/dbflow-validator/internal/orchestrator"
)

// fakeNetworkFactory returns a controllable NetworkFactory.
// It counts calls to factoryCalls and optionally returns an error.
type fakeNetworkFactory struct {
	calls    int
	name     string
	cleanup  func() error
	err      error
	cleaned  int
}

func (f *fakeNetworkFactory) factory() func(ctx context.Context) (string, func() error, error) {
	return func(_ context.Context) (string, func() error, error) {
		f.calls++
		if f.err != nil {
			return "", nil, f.err
		}
		cl := func() error { f.cleaned++; return nil }
		return f.name, cl, nil
	}
}

// --- Test 1: Early failure (clone) — NetworkFactory is NEVER called ---

// TestLazyNetwork_EarlyFailure_FactoryNotCalled verifies that when the run fails
// before container-start (e.g. clone fails), the NetworkFactory is NEVER invoked.
// This is the core property: no network is created on early exits.
func TestLazyNetwork_EarlyFailure_FactoryNotCalled(t *testing.T) {
	fn := &fakeNetworkFactory{name: "test-net-abc123"}
	deps := happyDeps(t)
	deps.NetworkFactory = fn.factory()
	// Force clone failure — exits before container-start.
	deps.Cloner = &fakeCloner{err: errors.New("auth failed")}

	rpt := orchestrator.Run(context.Background(), deps, testCfg())

	if rpt.Status != domain.StatusFailed {
		t.Fatalf("expected FAILED, got %v", rpt.Status)
	}

	if fn.calls != 0 {
		t.Errorf("NetworkFactory must NOT be called on early failure; got %d calls", fn.calls)
	}

	// Cleanup trace must show network n/a.
	step := findStepOrNil(rpt.Steps, "cleanup")
	if step == nil {
		t.Fatal("cleanup step not found")
	}
	trace := step.Trace
	if strings.Contains(trace, "eliminada") {
		t.Errorf("cleanup trace must not say 'eliminada' when network was never created;\ngot:\n%s", trace)
	}
	if !strings.Contains(trace, "n/a") && !strings.Contains(strings.ToLower(trace), "no se cre") {
		t.Errorf("cleanup trace must indicate network was not created (n/a);\ngot:\n%s", trace)
	}
}

// TestLazyNetwork_PreflightFailure_FactoryNotCalled verifies the same guarantee
// for preflight failures (very early exit before clone or container-start).
func TestLazyNetwork_PreflightFailure_FactoryNotCalled(t *testing.T) {
	fn := &fakeNetworkFactory{name: "test-net-abc123"}
	deps := happyDeps(t)
	deps.NetworkFactory = fn.factory()
	deps.Preflight = &fakePreflight{err: errors.New("docker not found")}

	rpt := orchestrator.Run(context.Background(), deps, testCfg())

	if rpt.Status != domain.StatusFailed {
		t.Fatalf("expected FAILED, got %v", rpt.Status)
	}
	if fn.calls != 0 {
		t.Errorf("NetworkFactory must NOT be called on preflight failure; got %d calls", fn.calls)
	}
}

// TestLazyNetwork_EngineGuardFailure_FactoryNotCalled verifies the guarantee for
// engine-guard failure (fails after clone but before container-start).
func TestLazyNetwork_EngineGuardFailure_FactoryNotCalled(t *testing.T) {
	fn := &fakeNetworkFactory{name: "test-net-abc123"}
	deps := happyDeps(t)
	deps.NetworkFactory = fn.factory()
	deps.Engine = &fakeEngineDetector{err: domain.ErrUnsupportedEngine}

	rpt := orchestrator.Run(context.Background(), deps, testCfg())

	if rpt.Status != domain.StatusFailed {
		t.Fatalf("expected FAILED, got %v", rpt.Status)
	}
	if fn.calls != 0 {
		t.Errorf("NetworkFactory must NOT be called on engine-guard failure; got %d calls", fn.calls)
	}
}

// --- Test 2: Full success path — network created, used, torn down last ---

// TestLazyNetwork_FullPath_FactoryCalledOnce verifies that on a full happy-path run,
// the NetworkFactory is called exactly once (at container-start).
func TestLazyNetwork_FullPath_FactoryCalledOnce(t *testing.T) {
	fn := &fakeNetworkFactory{name: "test-net-abc123"}
	deps := happyDeps(t)
	deps.NetworkFactory = fn.factory()

	rpt := orchestrator.Run(context.Background(), deps, testCfg())

	if rpt.Status != domain.StatusPassed {
		t.Fatalf("expected PASSED, got %v; steps: %+v", rpt.Status, rpt.Steps)
	}
	if fn.calls != 1 {
		t.Errorf("NetworkFactory must be called exactly once on full run; got %d calls", fn.calls)
	}
	// Network cleanup must have been invoked.
	if fn.cleaned != 1 {
		t.Errorf("network cleanup must be invoked after full run; got %d cleanup calls", fn.cleaned)
	}
}

// TestLazyNetwork_FullPath_CleanupTraceShowsEliminada verifies that the cleanup trace
// shows "eliminada" for the network on a full PASSED run (network was created and torn down).
func TestLazyNetwork_FullPath_CleanupTraceShowsEliminada(t *testing.T) {
	fn := &fakeNetworkFactory{name: "test-net-abc123"}
	deps := happyDeps(t)
	deps.NetworkFactory = fn.factory()

	rpt := orchestrator.Run(context.Background(), deps, testCfg())

	if rpt.Status != domain.StatusPassed {
		t.Fatalf("expected PASSED, got %v", rpt.Status)
	}

	step := findStepOrNil(rpt.Steps, "cleanup")
	if step == nil {
		t.Fatal("cleanup step not found")
	}
	trace := step.Trace
	if !strings.Contains(trace, "eliminada") {
		t.Errorf("cleanup trace must say 'eliminada' for network that was created;\ngot:\n%s", trace)
	}
}

// TestLazyNetwork_FullPath_NetworkCleanupIsLIFOLast verifies that the network cleanup
// runs AFTER the container cleanup (LIFO: network registered first = cleaned last).
func TestLazyNetwork_FullPath_NetworkCleanupIsLIFOLast(t *testing.T) {
	var order []string

	fn := &fakeNetworkFactory{name: "test-net-abc123"}
	cleanupFn := fn.factory()
	// Override factory to track order.
	networkFactory := func(ctx context.Context) (string, func() error, error) {
		name, _, err := cleanupFn(ctx)
		if err != nil {
			return "", nil, err
		}
		networkCleanup := func() error {
			order = append(order, "network")
			return nil
		}
		return name, networkCleanup, nil
	}

	deps := happyDeps(t)
	deps.NetworkFactory = networkFactory
	deps.DBProvider = &fakeDatabaseProvider{
		provider: &fakeContainerProvider{
			coords: domain.ContainerCoords{Host: "127.0.0.1", Port: 5432, User: "u", Password: "p", DBName: "db"},
			stopFn: func() error {
				order = append(order, "container")
				return nil
			},
		},
	}
	deps.Maven = &fakeMavenRunner{
		syncResult:     domain.StepResult{Status: domain.StepStatusPassed},
		rollbackResult: domain.StepResult{Status: domain.StepStatusPassed},
	}

	rpt := orchestrator.Run(context.Background(), deps, testCfg())

	if rpt.Status != domain.StatusPassed {
		t.Fatalf("expected PASSED, got %v; steps: %+v", rpt.Status, rpt.Steps)
	}

	// LIFO: network cleanup registered FIRST → runs LAST.
	// container registered AFTER network → runs BEFORE network.
	if len(order) < 2 {
		t.Fatalf("expected at least 2 cleanup calls (container + network), got %d: %v", len(order), order)
	}
	// Find positions.
	containerIdx := -1
	networkIdx := -1
	for i, v := range order {
		switch v {
		case "container":
			containerIdx = i
		case "network":
			networkIdx = i
		}
	}
	if containerIdx == -1 || networkIdx == -1 {
		t.Fatalf("missing container or network in cleanup order: %v", order)
	}
	if containerIdx > networkIdx {
		t.Errorf("container must be torn down BEFORE network (LIFO); order: %v", order)
	}
}

// --- Test 3: NetworkFactory error at container-start — clean failure ---

// TestLazyNetwork_FactoryError_CleanFailure verifies that when the NetworkFactory
// returns an error at container-start, the orchestrator fails the run cleanly
// with the container-start step marked as FAILED, without panicking or leaking state.
func TestLazyNetwork_FactoryError_CleanFailure(t *testing.T) {
	fn := &fakeNetworkFactory{
		name: "test-net-abc123",
		err:  errors.New("docker: cannot create network: insufficient permissions"),
	}
	deps := happyDeps(t)
	deps.NetworkFactory = fn.factory()

	rpt := orchestrator.Run(context.Background(), deps, testCfg())

	if rpt.Status != domain.StatusFailed {
		t.Fatalf("expected FAILED when network factory errors; got %v", rpt.Status)
	}

	// The container-start step must be in the report and marked FAILED.
	step := findStepOrNil(rpt.Steps, "container-start")
	if step == nil {
		t.Fatal("expected 'container-start' step in report when factory fails")
	}
	if step.Status != domain.StepStatusFailed {
		t.Errorf("container-start step must be FAILED when factory errors; got %v", step.Status)
	}
	if step.Error == "" {
		t.Error("container-start step must have a non-empty error message")
	}

	// Cleanup must still run (cleanup step present).
	cleanupStep := findStepOrNil(rpt.Steps, "cleanup")
	if cleanupStep == nil {
		t.Fatal("cleanup step must be present even when network factory fails")
	}

	// Network must show n/a in cleanup trace (factory errored — network was not created).
	trace := cleanupStep.Trace
	if strings.Contains(trace, "eliminada") {
		t.Errorf("cleanup trace must not say 'eliminada' when network factory errored;\ngot:\n%s", trace)
	}
}

// TestLazyNetwork_NilFactory_NoOp verifies that when NetworkFactory is nil,
// the orchestrator runs normally (no network; compatible with existing tests that omit it).
func TestLazyNetwork_NilFactory_NoOp(t *testing.T) {
	deps := happyDeps(t)
	deps.NetworkFactory = nil // explicitly nil — no network

	rpt := orchestrator.Run(context.Background(), deps, testCfg())

	if rpt.Status != domain.StatusPassed {
		t.Fatalf("expected PASSED with nil NetworkFactory, got %v; steps: %+v", rpt.Status, rpt.Steps)
	}

	// With nil factory, cleanup trace must show network as n/a.
	step := findStepOrNil(rpt.Steps, "cleanup")
	if step == nil {
		t.Fatal("cleanup step not found")
	}
	if strings.Contains(step.Trace, "eliminada") {
		t.Errorf("cleanup trace must not say 'eliminada' when no network is used;\ngot:\n%s", step.Trace)
	}
}

// TestLazyNetwork_ContainerStartStep_NetworkNameInTrace verifies that after a successful
// network creation, the container-start step trace includes the network name.
func TestLazyNetwork_ContainerStartStep_NetworkNameInTrace(t *testing.T) {
	const networkName = "dbflow-net-testxyz"
	fn := &fakeNetworkFactory{name: networkName}
	deps := happyDeps(t)
	deps.NetworkFactory = fn.factory()

	rpt := orchestrator.Run(context.Background(), deps, testCfg())

	if rpt.Status != domain.StatusPassed {
		t.Fatalf("expected PASSED, got %v; steps: %+v", rpt.Status, rpt.Steps)
	}

	step := findStepOrNil(rpt.Steps, "container-start")
	if step == nil {
		t.Fatal("container-start step not found")
	}
	if !strings.Contains(step.Trace, networkName) {
		t.Errorf("container-start trace must include network name %q;\ngot:\n%s", networkName, step.Trace)
	}
}
