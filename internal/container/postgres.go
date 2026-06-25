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

// postgresStartAttempts is the number of times Start will retry a transient
// Docker failure (e.g. "500 Internal Server Error" on container create/start).
const postgresStartAttempts = 3

// postgresStartBackoff is the sleep between postgres start retry attempts.
const postgresStartBackoff = time.Second

// postgresStarterFn is the injectable seam type for container start.
// It matches the signature of startOnce and is used in unit tests.
type postgresStarterFn func(ctx context.Context, networkName string) (domain.ContainerCoords, error)

const (
	postgresImage = "postgres:17.4"
	throwawayDB   = "validatordb"
	throwawayUser = "validator"
	throwawayPass = "v4lid4t0r_pass"
)

// PostgresProvider implements domain.ContainerProvider for an ephemeral postgres:17.4 container.
// testcontainers-go handles Ryuk-based cleanup automatically.
//
// The Docker network name is provided at Start time (not at construction), so the
// provider can be constructed before the network exists and the network is created lazily.
type PostgresProvider struct {
	container testcontainers.Container
}

// NewPostgresProvider returns a PostgresProvider ready to Start.
// The Docker network name is passed to Start when the network exists; pass "" to Start
// for host-only networking (no Docker network alias).
func NewPostgresProvider() *PostgresProvider {
	return &PostgresProvider{}
}

// postgresNetworkAlias is the in-network alias for the Postgres container.
// Maven containers connect to this alias at port 5432.
const postgresNetworkAlias = "postgres"

// startOnce performs a single attempt to launch the Postgres container.
// On failure it terminates any partially-created container (orphan cleanup) before
// returning the error, so callers that retry do not accumulate dangling containers.
func (p *PostgresProvider) startOnce(ctx context.Context, networkName string) (domain.ContainerCoords, error) {
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
	if networkName != "" {
		req.Networks = []string{networkName}
		req.NetworkAliases = map[string][]string{
			networkName: {postgresNetworkAlias},
		}
	}

	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		// testcontainers-go may return a non-nil container handle even on error;
		// terminate it to avoid orphaned containers before the next retry.
		if c != nil {
			_ = c.Terminate(ctx)
		}
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
	if networkName != "" {
		coords.AliasHost = postgresNetworkAlias
		coords.AliasPort = 5432
	}

	return coords, nil
}

// StartWithStarter launches the Postgres container using the provided starter function,
// retrying up to postgresStartAttempts times on transient errors.
//
// starter is an injectable seam used in unit tests. Production callers must pass
// p.startOnce (which uses real Docker). backoff controls the sleep between retries;
// production code passes postgresStartBackoff and tests pass 0 for fast execution.
func (p *PostgresProvider) StartWithStarter(ctx context.Context, networkName string, starter postgresStarterFn, backoff time.Duration) (domain.ContainerCoords, error) {
	var coords domain.ContainerCoords
	err := RetryDo(ctx, postgresStartAttempts, backoff, func() error {
		var e error
		coords, e = starter(ctx, networkName)
		return e
	})
	if err != nil {
		return domain.ContainerCoords{}, err
	}
	return coords, nil
}

// Start launches an ephemeral postgres:17.4 container on a random free host port
// and waits until Postgres logs that it is ready to accept connections.
//
// networkName is the Docker network to join; pass "" for host-only networking.
// When non-empty, the container joins that user-defined Docker network with alias
// "postgres" so Maven containers running in the same network can reach Postgres at
// postgres:5432. The returned ContainerCoords will have both Host:Port (admin/host
// path) and AliasHost:AliasPort (network path) set.
//
// Role creation (lb_<schema>, GRANT-target roles) is NOT done here — it is done
// by the orchestrator after schema extraction from the archetype DDL, so the set
// of roles is determined from the actual repo contents, not hardcoded.
//
// Transient Docker failures (e.g. "500 Internal Server Error" on container
// create/start) are retried up to postgresStartAttempts times with
// postgresStartBackoff between attempts. A partially-created container from a
// failed attempt is terminated before the next retry (orphan cleanup).
func (p *PostgresProvider) Start(ctx context.Context, networkName string) (domain.ContainerCoords, error) {
	return p.StartWithStarter(ctx, networkName, p.startOnce, postgresStartBackoff)
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
