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
type ContainerCoords struct {
	Host     string
	Port     int
	User     string
	Password string
	DBName   string
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
