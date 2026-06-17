package engine

import (
	"fmt"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
)

// ProviderFor returns the DatabaseProvider for a detected engine.
// Adding a new engine requires only one case here and one new provider file.
func ProviderFor(e Engine) (domain.DatabaseProvider, error) {
	switch e {
	case EnginePostgres:
		return &postgresProvider{}, nil
	default:
		return nil, fmt.Errorf("%w: no provider for engine %q", domain.ErrUnsupportedEngine, e)
	}
}
