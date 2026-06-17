package container

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // register "pgx" driver for database/sql

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	postgresImage = "postgres:17.4"
	throwawayDB   = "validatordb"
	throwawayUser = "validator"
	throwawayPass = "v4lid4t0r_pass"
)

// PostgresProvider implements domain.ContainerProvider for an ephemeral postgres:17.4 container.
// testcontainers-go handles Ryuk-based cleanup automatically.
type PostgresProvider struct {
	container testcontainers.Container
}

// NewPostgresProvider returns a PostgresProvider ready to Start.
func NewPostgresProvider() *PostgresProvider {
	return &PostgresProvider{}
}

// Start launches an ephemeral postgres:17.4 container on a random free host port
// and waits until Postgres logs that it is ready to accept connections.
//
// Role creation (lb_<schema>, GRANT-target roles) is NOT done here — it is done
// by the orchestrator after schema extraction from the archetype DDL, so the set
// of roles is determined from the actual repo contents, not hardcoded.
func (p *PostgresProvider) Start(ctx context.Context) (domain.ContainerCoords, error) {
	req := testcontainers.ContainerRequest{
		Image:        postgresImage,
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_DB":       throwawayDB,
			"POSTGRES_USER":     throwawayUser,
			"POSTGRES_PASSWORD": throwawayPass,
		},
		// Wait until the ready message appears twice (primary + health).
		WaitingFor: wait.ForLog("database system is ready to accept connections").
			WithOccurrence(2).
			WithStartupTimeout(120 * time.Second),
	}

	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return domain.ContainerCoords{}, fmt.Errorf("%w: %v", domain.ErrContainerFailed, err)
	}

	host, err := c.Host(ctx)
	if err != nil {
		_ = c.Terminate(ctx)
		return domain.ContainerCoords{}, fmt.Errorf("%w: get host: %v", domain.ErrContainerFailed, err)
	}

	mappedPort, err := c.MappedPort(ctx, "5432")
	if err != nil {
		_ = c.Terminate(ctx)
		return domain.ContainerCoords{}, fmt.Errorf("%w: get port: %v", domain.ErrContainerFailed, err)
	}

	p.container = c
	return domain.ContainerCoords{
		Host:     host,
		Port:     int(mappedPort.Num()),
		User:     throwawayUser,
		Password: throwawayPass,
		DBName:   throwawayDB,
	}, nil
}

// Stop terminates and removes the ephemeral container.
// Idempotent — safe to call when no container is running.
func (p *PostgresProvider) Stop(ctx context.Context) error {
	if p.container == nil {
		return nil
	}
	if err := p.container.Terminate(ctx); err != nil {
		return fmt.Errorf("terminate postgres container: %w", err)
	}
	p.container = nil
	return nil
}

// Ping opens a database/sql connection using the pgx driver and executes SELECT 1.
// The DSN must be in the pgx connection-string format: postgres://user:pass@host:port/db
func Ping(ctx context.Context, dsn string) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open postgres connection: %w", err)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("postgres ping: %w", err)
	}
	return nil
}

// Ensure PostgresProvider satisfies domain.ContainerProvider at compile time.
var _ domain.ContainerProvider = (*PostgresProvider)(nil)
