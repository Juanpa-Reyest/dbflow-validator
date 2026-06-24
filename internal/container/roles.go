package container

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
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
// Errors for individual roles are logged and captured in the trace Output rather
// than being fatal — a single failure does not abort role creation for remaining roles.
// Returns an error only when the database connection itself cannot be established.
// Returns one CommandTrace per role: Command = SQL executed, Output = command tag or error.
func CreateRolesIfNotExist(ctx context.Context, dsn string, roles []string) ([]domain.CommandTrace, error) {
	if len(roles) == 0 {
		return nil, nil
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("CreateRolesIfNotExist: open connection: %w", err)
	}
	defer db.Close()

	traces := make([]domain.CommandTrace, 0, len(roles))
	for _, r := range roles {
		stmt := fmt.Sprintf(`DO $$
BEGIN
  CREATE ROLE %s;
EXCEPTION WHEN duplicate_object THEN
  NULL;
END
$$;`, r)
		result, execErr := db.ExecContext(ctx, stmt)
		if execErr != nil {
			// Log and continue — a failed role creation should not abort the
			// entire validation; it will surface as a GRANT failure later.
			slog.Warn("failed to create role", "role", r, "err", execErr)
			traces = append(traces, domain.CommandTrace{Command: stmt, Output: execErr.Error()})
		} else {
			slog.Debug("ensured role exists", "role", r)
			tag, _ := result.RowsAffected()
			traces = append(traces, domain.CommandTrace{Command: stmt, Output: fmt.Sprintf("DO (rows affected: %d)", tag)})
		}
	}
	return traces, nil
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
// Returns a CommandTrace with the SQL executed and the driver result (command tag or error).
func GrantConnectCreateOnDatabase(ctx context.Context, adminDSN, dbName, lbUsername string) (domain.CommandTrace, error) {
	stmt := BuildGrantConnectCreateOnDatabaseSQL(dbName, lbUsername)
	db, err := sql.Open("pgx", adminDSN)
	if err != nil {
		return domain.CommandTrace{Command: stmt, Output: err.Error()},
			fmt.Errorf("GrantConnectCreateOnDatabase: open connection: %w", err)
	}
	defer db.Close()
	result, err := db.ExecContext(ctx, stmt)
	if err != nil {
		return domain.CommandTrace{Command: stmt, Output: err.Error()},
			fmt.Errorf("GrantConnectCreateOnDatabase: %w", err)
	}
	slog.Debug("granted CONNECT, CREATE on database", "db", dbName, "user", lbUsername)
	tag, _ := result.RowsAffected()
	return domain.CommandTrace{Command: stmt, Output: fmt.Sprintf("GRANT (rows affected: %d)", tag)}, nil
}

// CreateLbBookkeepingSchema creates the schema named after lbUsername and sets
// its owner to lbUsername. This provides Liquibase with its default search_path
// ("$user") for DATABASECHANGELOG storage. The operation uses the admin DSN
// (superuser) and is idempotent.
// Returns ONE CommandTrace joining both SQL statements with the combined driver result.
func CreateLbBookkeepingSchema(ctx context.Context, adminDSN, lbUsername string) (domain.CommandTrace, error) {
	stmts := BuildCreateLbBookkeepingSchemaSQL(lbUsername)
	db, err := sql.Open("pgx", adminDSN)
	if err != nil {
		combinedCmd := strings.Join(stmts, "; ")
		return domain.CommandTrace{Command: combinedCmd, Output: err.Error()},
			fmt.Errorf("CreateLbBookkeepingSchema: open connection: %w", err)
	}
	defer db.Close()

	combinedCmd := strings.Join(stmts, "; ")
	var outputParts []string
	for _, stmt := range stmts {
		result, execErr := db.ExecContext(ctx, stmt)
		if execErr != nil {
			return domain.CommandTrace{Command: combinedCmd, Output: strings.Join(append(outputParts, execErr.Error()), "; ")},
				fmt.Errorf("CreateLbBookkeepingSchema %q: %w", stmt, execErr)
		}
		tag, _ := result.RowsAffected()
		outputParts = append(outputParts, fmt.Sprintf("DO (rows affected: %d)", tag))
	}
	slog.Debug("created lb bookkeeping schema", "schema", lbUsername)
	return domain.CommandTrace{Command: combinedCmd, Output: strings.Join(outputParts, "; ")}, nil
}

// CreateLbUser creates a login-capable role named lb_<schema> in the ephemeral
// Postgres using the admin DSN. The role is created with LOGIN and the given
// password, using IF NOT EXISTS for idempotency.
// Returns a CommandTrace with the SQL executed (password REDACTED) and the driver result.
func CreateLbUser(ctx context.Context, adminDSN, lbUsername, lbPassword string) (domain.CommandTrace, error) {
	db, err := sql.Open("pgx", adminDSN)
	// Build the redacted command for trace (never include the real password).
	redactedStmt := fmt.Sprintf(`
DO $$
BEGIN
  CREATE ROLE %s WITH LOGIN PASSWORD '[REDACTED]';
EXCEPTION WHEN duplicate_object THEN
  NULL;
END
$$;`, lbUsername)
	if err != nil {
		return domain.CommandTrace{Command: redactedStmt, Output: err.Error()},
			fmt.Errorf("CreateLbUser: open connection: %w", err)
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

	result, err := db.ExecContext(ctx, stmt)
	if err != nil {
		return domain.CommandTrace{Command: redactedStmt, Output: err.Error()},
			fmt.Errorf("CreateLbUser %q: %w", lbUsername, err)
	}
	slog.Debug("ensured lb user exists", "user", lbUsername)
	tag, _ := result.RowsAffected()
	return domain.CommandTrace{Command: redactedStmt, Output: fmt.Sprintf("DO (rows affected: %d)", tag)}, nil
}
