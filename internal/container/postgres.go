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
//
// When networkName is non-empty, the container joins that user-defined Docker network
// and registers itself with the alias "postgres" so the Maven container can reach it
// at postgres:5432 from within the same network.
type PostgresProvider struct {
	container   testcontainers.Container
	networkName string // Docker network to join; empty means host networking only.
}

// NewPostgresProvider returns a PostgresProvider ready to Start.
// Pass "" for networkName to use the default host-only setup (no Docker network).
// Pass a non-empty network name (from NewNetwork) to join that network with alias "postgres".
func NewPostgresProvider(networkName string) *PostgresProvider {
	return &PostgresProvider{networkName: networkName}
}

// postgresNetworkAlias is the in-network alias for the Postgres container.
// Maven containers connect to this alias at port 5432.
const postgresNetworkAlias = "postgres"

// Start launches an ephemeral postgres:17.4 container on a random free host port
// and waits until Postgres logs that it is ready to accept connections.
//
// When the provider was created with a non-empty networkName, the container also
// joins that user-defined Docker network with alias "postgres" so it is reachable
// at postgres:5432 from within the network. The returned ContainerCoords will
// have both Host:Port (admin/host path) and AliasHost:AliasPort (network path) set.
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

	// When a Docker network is configured, join it with the "postgres" alias so
	// Maven containers running in the same network can reach Postgres at postgres:5432.
	if p.networkName != "" {
		req.Networks = []string{p.networkName}
		req.NetworkAliases = map[string][]string{
			p.networkName: {postgresNetworkAlias},
		}
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

	coords := domain.ContainerCoords{
		Host:     host,
		Port:     int(mappedPort.Num()),
		User:     throwawayUser,
		Password: throwawayPass,
		DBName:   throwawayDB,
	}

	// Populate alias coords when a Docker network is in use.
	if p.networkName != "" {
		coords.AliasHost = postgresNetworkAlias
		coords.AliasPort = 5432
	}

	return coords, nil
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
