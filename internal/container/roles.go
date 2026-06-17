package container

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
)

// ExecSQL opens a database connection using dsn and executes a single SQL statement.
func ExecSQL(ctx context.Context, dsn, stmt string) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("ExecSQL: open connection: %w", err)
	}
	defer db.Close()

	if _, err := db.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("ExecSQL %q: %w", stmt, err)
	}
	return nil
}

// BuildCreateRolesSQL returns a slice of idempotent CREATE ROLE SQL statements
// for the given role names. Postgres 9.6+ supports IF NOT EXISTS on CREATE ROLE.
func BuildCreateRolesSQL(roles []string) []string {
	if len(roles) == 0 {
		return nil
	}
	stmts := make([]string, len(roles))
	for i, r := range roles {
		stmts[i] = fmt.Sprintf("CREATE ROLE IF NOT EXISTS %s", r)
	}
	return stmts
}

// CreateRolesIfNotExist opens a database connection using dsn and executes an
// idempotent CREATE ROLE IF NOT EXISTS statement for each role in roles.
// Errors for individual roles are logged and skipped so that a single failure
// does not abort role creation for the remaining roles.
// Returns an error only when the database connection itself cannot be established.
func CreateRolesIfNotExist(ctx context.Context, dsn string, roles []string) error {
	if len(roles) == 0 {
		return nil
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("CreateRolesIfNotExist: open connection: %w", err)
	}
	defer db.Close()

	for _, r := range roles {
		stmt := fmt.Sprintf("CREATE ROLE IF NOT EXISTS %s", r)
		if _, execErr := db.ExecContext(ctx, stmt); execErr != nil {
			// Log and continue — a failed role creation should not abort the
			// entire validation; it will surface as a GRANT failure later.
			slog.Warn("failed to create role", "role", r, "err", execErr)
		} else {
			slog.Debug("ensured role exists", "role", r)
		}
	}
	return nil
}

// CreateLbUser creates a login-capable role named lb_<schema> in the ephemeral
// Postgres using the admin DSN. The role is created with LOGIN and the given
// password, using IF NOT EXISTS for idempotency.
func CreateLbUser(ctx context.Context, adminDSN, lbUsername, lbPassword string) error {
	db, err := sql.Open("pgx", adminDSN)
	if err != nil {
		return fmt.Errorf("CreateLbUser: open connection: %w", err)
	}
	defer db.Close()

	// Use DO block for idempotency: IF NOT EXISTS is not available on CREATE USER
	// in all Postgres versions for roles with LOGIN. The DO block catches
	// duplicate_object gracefully.
	stmt := fmt.Sprintf(`
DO $$
BEGIN
  CREATE ROLE %s WITH LOGIN PASSWORD '%s';
EXCEPTION WHEN duplicate_object THEN
  NULL;
END
$$;`, lbUsername, lbPassword)

	if _, err := db.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("CreateLbUser %q: %w", lbUsername, err)
	}
	slog.Debug("ensured lb user exists", "user", lbUsername)
	return nil
}
