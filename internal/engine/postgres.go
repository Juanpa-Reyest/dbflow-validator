package engine

import (
	"context"
	"fmt"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
)

const (
	postgresImage = "postgres:17.4"
)

// postgresProvider implements domain.DatabaseProvider for PostgreSQL.
// Container lifecycle is delegated to the container package at wire-up time.
type postgresProvider struct {
	containerProvider domain.ContainerProvider
}

func (p *postgresProvider) Image() string {
	return postgresImage
}

func (p *postgresProvider) ContainerProvider() domain.ContainerProvider {
	return p.containerProvider
}

func (p *postgresProvider) DSN(coords domain.ContainerCoords) string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=disable",
		coords.User, coords.Password, coords.Host, coords.Port, coords.DBName,
	)
}

func (p *postgresProvider) Ping(ctx context.Context, dsn string) error {
	// Real ping via database/sql is wired at integration time.
	// This stub satisfies the interface; the container package provides a real one.
	return fmt.Errorf("ping not implemented at engine layer; inject via container package")
}

// Ensure postgresProvider satisfies domain.DatabaseProvider at compile time.
var _ domain.DatabaseProvider = (*postgresProvider)(nil)
