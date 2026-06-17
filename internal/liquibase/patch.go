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
func (pt *Patcher) Patch(path string, coords domain.ContainerCoords) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	props, err := Parse(data)
	if err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}

	jdbcURL := fmt.Sprintf("jdbc:postgresql://%s:%d/%s", coords.Host, coords.Port, coords.DBName)
	props.Set("url", jdbcURL)
	props.Set("username", coords.User)
	props.Set("password", coords.Password)
	props.Set("driver", "org.postgresql.Driver")

	rendered := Render(props)
	if err := os.WriteFile(path, rendered, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// Ensure Patcher satisfies domain.PropertiesPatcher at compile time.
var _ domain.PropertiesPatcher = (*Patcher)(nil)
