package liquibase

import (
	"fmt"
	"os"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
)

// PatchCoords is a convenience alias kept for tests that predated the domain type.
// New code should use domain.ContainerCoords directly.
type PatchCoords = domain.ContainerCoords

// Patcher overwrites liquibase.properties with ephemeral container coordinates.
type Patcher struct{}

// NewPatcher returns a Patcher ready to use.
func NewPatcher() *Patcher { return &Patcher{} }

// Patch reads the properties file at path, injects the ephemeral coordinates,
// and writes the result back to the same path. Extra keys are preserved (lossless).
// The file at path must exist; an error is returned otherwise.
// Returns the list of key-value changes made (before→after per key).
// IMPORTANT: the caller must apply ScrubSecrets on the password values before
// writing them into StepResult.Trace — this function returns the raw values.
func (pt *Patcher) Patch(path string, coords domain.ContainerCoords) ([]domain.PropChange, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	props, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	// Use the Docker network alias when available (Maven container path),
	// otherwise fall back to the host-mapped port (admin / legacy path).
	jdbcHost := coords.Host
	jdbcPort := coords.Port
	if coords.AliasHost != "" {
		jdbcHost = coords.AliasHost
		jdbcPort = coords.AliasPort
	}
	jdbcURL := fmt.Sprintf("jdbc:postgresql://%s:%d/%s", jdbcHost, jdbcPort, coords.DBName)

	// Track before→after for each key we set.
	type kv struct{ key, val string }
	newValues := []kv{
		{"url", jdbcURL},
		{"username", coords.User},
		{"password", coords.Password},
		{"driver", "org.postgresql.Driver"},
	}

	changes := make([]domain.PropChange, 0, len(newValues))
	for _, nv := range newValues {
		before := props.Get(nv.key)
		props.Set(nv.key, nv.val)
		changes = append(changes, domain.PropChange{Key: nv.key, Before: before, After: nv.val})
	}

	rendered := Render(props)
	if err := os.WriteFile(path, rendered, 0o600); err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	return changes, nil
}

// Ensure Patcher satisfies domain.PropertiesPatcher at compile time.
var _ domain.PropertiesPatcher = (*Patcher)(nil)
