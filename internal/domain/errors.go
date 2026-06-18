package domain

import "errors"

// Secret is a string that redacts itself in all display/serialization contexts.
// Use Reveal() to access the underlying value — the only intentional exposure point.
type Secret struct {
	v string
}

// NewSecret wraps a raw string as a Secret.
func NewSecret(v string) Secret {
	return Secret{v: v}
}

// String satisfies fmt.Stringer — always returns "***".
func (s Secret) String() string { return "***" }

// GoString satisfies fmt.GoStringer — always returns "***".
func (s Secret) GoString() string { return "***" }

// MarshalJSON satisfies json.Marshaler — always serializes as "***".
func (s Secret) MarshalJSON() ([]byte, error) {
	return []byte(`"***"`), nil
}

// Reveal returns the raw underlying value. Call only at the single point where
// the secret must be used (e.g., building the authenticated clone URL).
func (s Secret) Reveal() string { return s.v }

// Sentinel errors for the domain.
var (
	ErrUnsupportedEngine  = errors.New("unsupported database engine")
	ErrPreflight          = errors.New("preflight check failed")
	ErrReadinessTimeout   = errors.New("database readiness timeout")
	ErrSyncFailed         = errors.New("sync step failed")
	ErrRollbackFailed     = errors.New("rollback step failed")
	ErrNoIncludes         = errors.New("no <include> elements found in master-changelog")
	ErrNoFirstTag         = errors.New("no <tagDatabase> element found in included changelogs")
	ErrCloneFailed        = errors.New("repository clone failed")
	ErrContainerFailed    = errors.New("container start failed")
	ErrPropertiesMissing  = errors.New("liquibase.properties file not found")
	// ErrNoPendingSQL is returned when the local SQLInput directory is missing or
	// contains no .sql files. The tool must exit with the config/usage code (2).
	ErrNoPendingSQL = errors.New("no pending SQL found — nothing to validate")
)
