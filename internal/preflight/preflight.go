// Package preflight verifies that required host binaries are on the PATH
// before any external operation (clone, container start, Maven) is attempted.
package preflight

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
)

// execLookPath is the default production LookPath using os/exec.
var execLookPath = exec.LookPath

// requiredTools lists the binaries that must exist on the host PATH.
var requiredTools = []string{"docker", "mvn", "git", "java"}

// Preflight checks host binary availability using an injectable LookPath func.
// Inject os/exec.LookPath in production; inject a fake in tests.
type Preflight struct {
	lookPath func(string) (string, error)
}

// New returns a Preflight that uses the provided LookPath func.
// Pass nil to use exec.LookPath from the standard library.
func New(lookPath func(string) (string, error)) *Preflight {
	if lookPath == nil {
		lookPath = execLookPath
	}
	return &Preflight{lookPath: lookPath}
}

// Check verifies all required host tools are present.
// Returns (statuses, nil) when all are found, or (nil, error) for the first
// missing tool with a distinct, actionable error message.
func (p *Preflight) Check(_ context.Context) ([]domain.ToolStatus, error) {
	statuses := make([]domain.ToolStatus, 0, len(requiredTools))

	for _, name := range requiredTools {
		path, err := p.lookPath(name)
		if err != nil {
			return nil, fmt.Errorf("%w: %q not found on PATH — install it and re-run: %v",
				domain.ErrPreflight, name, err)
		}
		statuses = append(statuses, domain.ToolStatus{
			Name:  name,
			Found: true,
			Path:  path,
		})
	}

	return statuses, nil
}
