// Package orchestrator wires the full validation flow and owns cleanup lifecycle.
package orchestrator

import "sync"

// CleanFunc is a cleanup function that returns an error if cleanup partially fails.
type CleanFunc func() error

// entry wraps a CleanFunc with a once guard to prevent double execution.
type entry struct {
	fn   CleanFunc
	once sync.Once
}

// CleanupRegistry is a LIFO run-once cleanup registry.
// Register resources in creation order; RunAll executes them in reverse (LIFO).
// Every CleanFunc is guaranteed to run at most once even under concurrent calls.
type CleanupRegistry struct {
	mu      sync.Mutex
	entries []*entry
	ran     bool
}

// NewCleanupRegistry returns an empty CleanupRegistry.
func NewCleanupRegistry() *CleanupRegistry {
	return &CleanupRegistry{}
}

// Register adds fn to the registry. It will be called in LIFO order by RunAll.
func (r *CleanupRegistry) Register(fn CleanFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, &entry{fn: fn})
}

// RunAll executes all registered functions in LIFO order.
// It is idempotent — a second call is a no-op.
// Errors from individual cleaners are collected and returned; a failed cleaner
// does NOT prevent subsequent cleaners from running.
func (r *CleanupRegistry) RunAll() []error {
	r.mu.Lock()
	if r.ran {
		r.mu.Unlock()
		return nil
	}
	r.ran = true
	// Snapshot in LIFO order.
	snapshot := make([]*entry, len(r.entries))
	for i, e := range r.entries {
		snapshot[len(r.entries)-1-i] = e
	}
	r.mu.Unlock()

	var errs []error
	for _, e := range snapshot {
		var runErr error
		e.once.Do(func() {
			runErr = e.fn()
		})
		if runErr != nil {
			errs = append(errs, runErr)
		}
	}
	return errs
}
