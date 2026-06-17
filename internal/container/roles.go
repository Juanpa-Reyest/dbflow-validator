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

// BuildCreateRolesSQL returns a slice of idempotent role-creation SQL statements
// for the given role names. PostgreSQL does NOT support CREATE ROLE IF NOT EXISTS,
// so each statement uses a DO block that catches duplicate_object gracefully.
func BuildCreateRolesSQL(roles []string) []string {
	if len(roles) == 0 {
		return nil
	}
	stmts := make([]string, len(roles))
	for i, r := range roles {
		stmts[i] = fmt.Sprintf(`DO $$
BEGIN
  CREATE ROLE %s;
EXCEPTION WHEN duplicate_object THEN
  NULL;
END
$$;`, r)
	}
	return stmts
}

// CreateRolesIfNotExist opens a database connection using dsn and executes an
// idempotent role-creation DO block for each role in roles.
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
		stmt := fmt.Sprintf(`DO $$
BEGIN
  CREATE ROLE %s;
EXCEPTION WHEN duplicate_object THEN
  NULL;
END
$$;`, r)
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

// BuildGrantConnectCreateOnDatabaseSQL returns the SQL statement that grants the
// lb user CONNECT and CREATE privileges on the throwaway database.
// This mirrors the ambientacion.sql pattern:
//
//	GRANT CONNECT, CREATE ON DATABASE <db> TO <user>
//
// Both privileges are required: CONNECT to establish sessions, CREATE to allow
// the lb user to create the application schema in that database.
func BuildGrantConnectCreateOnDatabaseSQL(dbName, username string) string {
	return fmt.Sprintf("GRANT CONNECT, CREATE ON DATABASE %s TO %s", dbName, username)
}

// BuildCreateLbBookkeepingSchemaSQL returns the SQL statements that create the
// lb_<schema> bookkeeping schema and set its owner to the lb user.
// This mirrors the ambientacion.sql pattern:
//
//	CREATE SCHEMA scliquibase;
//	ALTER SCHEMA scliquibase OWNER TO scliquibase;
//
// Liquibase stores DATABASECHANGELOG in the user's default search_path ("$user"),
// which resolves to the schema matching the username — hence the schema and the
// username must be identical.
// A DO block handles duplicate_object so the operation is idempotent.
func BuildCreateLbBookkeepingSchemaSQL(lbUsername string) []string {
	return []string{
		// Create bookkeeping schema named after the lb user (idempotent).
		fmt.Sprintf(`DO $$
BEGIN
  CREATE SCHEMA %s;
EXCEPTION WHEN duplicate_schema THEN
  NULL;
END
$$;`, lbUsername),
		// Set ownership so the lb user has full control over its schema.
		fmt.Sprintf("ALTER SCHEMA %s OWNER TO %s", lbUsername, lbUsername),
	}
}

// GrantConnectCreateOnDatabase executes GRANT CONNECT, CREATE ON DATABASE <db> TO <user>
// using the admin DSN. Both privileges are required for the lb user:
// CONNECT to establish sessions, CREATE to create the application schema.
func GrantConnectCreateOnDatabase(ctx context.Context, adminDSN, dbName, lbUsername string) error {
	stmt := BuildGrantConnectCreateOnDatabaseSQL(dbName, lbUsername)
	db, err := sql.Open("pgx", adminDSN)
	if err != nil {
		return fmt.Errorf("GrantConnectCreateOnDatabase: open connection: %w", err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("GrantConnectCreateOnDatabase: %w", err)
	}
	slog.Debug("granted CONNECT, CREATE on database", "db", dbName, "user", lbUsername)
	return nil
}

// CreateLbBookkeepingSchema creates the schema named after lbUsername and sets
// its owner to lbUsername. This provides Liquibase with its default search_path
// ("$user") for DATABASECHANGELOG storage. The operation uses the admin DSN
// (superuser) and is idempotent.
func CreateLbBookkeepingSchema(ctx context.Context, adminDSN, lbUsername string) error {
	stmts := BuildCreateLbBookkeepingSchemaSQL(lbUsername)
	db, err := sql.Open("pgx", adminDSN)
	if err != nil {
		return fmt.Errorf("CreateLbBookkeepingSchema: open connection: %w", err)
	}
	defer db.Close()
	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("CreateLbBookkeepingSchema %q: %w", stmt, err)
		}
	}
	slog.Debug("created lb bookkeeping schema", "schema", lbUsername)
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
