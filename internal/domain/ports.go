package domain

import (
	"context"
	"io"
)

// ToolStatus reports whether a host tool is present.
type ToolStatus struct {
	Name  string
	Found bool
	Path  string
}

// PreflightChecker verifies that required host tools are available.
type PreflightChecker interface {
	Check(ctx context.Context) ([]ToolStatus, error)
}

// CloneOptions parameterises a git clone operation.
type CloneOptions struct {
	RepoURL    string
	Branch     string
	Token      Secret
	DestDir    string
}

// Cloner clones a remote git repository into a local directory.
type Cloner interface {
	Clone(ctx context.Context, opts CloneOptions) (cloneRoot string, err error)
}

// ContainerCoords holds ephemeral container connection details.
//
// DUAL-COORDINATES design: the same Postgres instance is accessed via two paths:
//   - Host:Port     — mapped host port; used by the Go process for readiness probe
//                     and admin provisioning (lb_<schema> user, GRANT-target roles,
//                     bookkeeping schema). Go resolves "127.0.0.1" locally.
//   - AliasHost:AliasPort — Docker network alias ("postgres":5432); used ONLY in
//                     the injected liquibase.properties so the Maven container
//                     (running inside the same Docker network) can reach Postgres.
//                     Host Go cannot resolve "postgres"; Maven container can.
//
// Both coordinates point at the SAME database instance — they differ only in how
// the TCP path is resolved (host NAT vs. Docker network alias).
type ContainerCoords struct {
	// Host is the host-side address (typically 127.0.0.1) at MappedPort.
	// Used by the Go process for admin DSN, readiness probe, and provisioning.
	Host string
	// Port is the host-side mapped port returned by testcontainers MappedPort.
	Port int
	// AliasHost is the Docker network alias for the Postgres container
	// (e.g. "postgres"). Used in liquibase.properties for the Maven container.
	AliasHost string
	// AliasPort is the container-internal port for the alias path (always 5432).
	AliasPort int
	User      string
	Password  string
	DBName    string
}

// ContainerProvider starts and stops ephemeral database containers.
type ContainerProvider interface {
	Start(ctx context.Context) (ContainerCoords, error)
	Stop(ctx context.Context) error
}

// DatabaseProvider encapsulates engine-specific container and DSN logic.
type DatabaseProvider interface {
	Image() string
	ContainerProvider() ContainerProvider
	DSN(coords ContainerCoords) string
	Ping(ctx context.Context, dsn string) error
}

// PropertiesPatcher overwrites liquibase.properties with ephemeral container coords.
type PropertiesPatcher interface {
	Patch(path string, coords ContainerCoords) error
}

// EngineDetector reads liquibase.properties and identifies the target DB engine.
type EngineDetector interface {
	Detect(propsPath string) (string, error)
}

// TagResolver extracts the first rollback tag from a master-changelog.
type TagResolver interface {
	FirstTag(cloneRoot string) (string, error)
}

// MavenRunner executes Maven goals in a cloned repository.
type MavenRunner interface {
	Run(ctx context.Context, cloneRoot string, goal string, params []string, out io.Writer) (StepResult, error)
}

// PreSyncValidator is an optional extensibility seam for plugging in a SQL-rules
// validation step BEFORE the ephemeral sync runs — mirroring the real pipeline's
// validate → validate-ephemeral order.
//
// Example future implementation: run the library-script-validator JAR against the
// cloned SQL files, parse the JSON report, and abort if globalSummary.status is
// FAIL or ERROR.
//
// The default (no-op) implementation is provided by NoOpPreSyncValidator.
// Implementors receive the cloneRoot directory and must return a non-nil error to
// abort the pipeline at the pre-sync-validate step.
type PreSyncValidator interface {
	ValidatePreSync(ctx context.Context, cloneRoot string) error
}

// NoOpPreSyncValidator is the default PreSyncValidator that always passes.
// Wire this when no external rules-validator is configured.
type NoOpPreSyncValidator struct{}

func (NoOpPreSyncValidator) ValidatePreSync(_ context.Context, _ string) error { return nil }

// Overlayer copies the developer's local SQLInput tree into the freshly-cloned
// repository's SQLInput directory before sync.
//
// Apply clears destSQLInputDir first (clear-then-copy semantics), then
// recursively copies all files from srcDir, preserving subdirectory hierarchy.
// Only regular .sql files are copied; symlinks and device files are skipped.
//
// Returns ErrNoPendingSQL (wrapped) if srcDir contains no .sql files.
// Returns (copied int, err error) where copied is the number of files written.
type Overlayer interface {
	Apply(srcDir, destSQLInputDir string) (copied int, err error)
}
