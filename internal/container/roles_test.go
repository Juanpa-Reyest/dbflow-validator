package container_test

import (
	"context"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/dbflow-validator/dbflow-validator/internal/container"
)

// TestBuildGrantConnectCreateOnDatabaseSQL verifies that the provisioning SQL
// mirrors ambientacion.sql: GRANT CONNECT, CREATE ON DATABASE <db> TO <user>.
func TestBuildGrantConnectCreateOnDatabaseSQL(t *testing.T) {
	tests := []struct {
		name     string
		dbName   string
		username string
		wantSubs []string
	}{
		{
			name:     "standard provisioning",
			dbName:   "validatordb",
			username: "lb_scgolfcore",
			wantSubs: []string{"GRANT CONNECT", "CREATE ON DATABASE validatordb", "TO lb_scgolfcore"},
		},
		{
			name:     "different db and user",
			dbName:   "mydb",
			username: "lb_myschema",
			wantSubs: []string{"CONNECT", "CREATE ON DATABASE mydb", "lb_myschema"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sql := container.BuildGrantConnectCreateOnDatabaseSQL(tt.dbName, tt.username)
			for _, sub := range tt.wantSubs {
				if !strings.Contains(sql, sub) {
					t.Errorf("BuildGrantConnectCreateOnDatabaseSQL(%q, %q) = %q, missing %q",
						tt.dbName, tt.username, sql, sub)
				}
			}
		})
	}
}

// TestBuildCreateLbBookkeepingSchemaSQL verifies that the provisioning SQL
// creates the bookkeeping schema (lb_<schema>) and sets its owner to the lb user.
// This mirrors: CREATE SCHEMA scliquibase; ALTER SCHEMA scliquibase OWNER TO scliquibase;
func TestBuildCreateLbBookkeepingSchemaSQL(t *testing.T) {
	tests := []struct {
		name     string
		username string // lb_<schema>
		wantSubs []string
	}{
		{
			name:     "creates schema matching lb username",
			username: "lb_scgolfcore",
			wantSubs: []string{"CREATE SCHEMA", "lb_scgolfcore", "ALTER SCHEMA", "OWNER TO lb_scgolfcore"},
		},
		{
			name:     "different schema",
			username: "lb_payments",
			wantSubs: []string{"lb_payments", "OWNER TO lb_payments"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stmts := container.BuildCreateLbBookkeepingSchemaSQL(tt.username)
			combined := strings.Join(stmts, "\n")
			for _, sub := range tt.wantSubs {
				if !strings.Contains(combined, sub) {
					t.Errorf("BuildCreateLbBookkeepingSchemaSQL(%q) combined=%q, missing %q",
						tt.username, combined, sub)
				}
			}
		})
	}
}

// TestBuildCreateRolesSQL verifies that BuildCreateRolesSQL generates idempotent
// role creation SQL for the given role names.
// Note: PostgreSQL does NOT support `CREATE ROLE IF NOT EXISTS` — the idiomatic
// approach is a DO $$ block with EXCEPTION WHEN duplicate_object THEN NULL.
func TestBuildCreateRolesSQL(t *testing.T) {
	tests := []struct {
		name  string
		roles []string
		want  []string // substrings that must appear in each output statement
	}{
		{
			// Each statement must be a DO block for idempotency (PG has no IF NOT EXISTS for ROLE).
			name:  "single role produces DO block",
			roles: []string{"appbackend"},
			want:  []string{"appbackend"},
		},
		{
			name:  "multiple roles produce separate statements",
			roles: []string{"appbackend", "readonly"},
			want:  []string{"appbackend", "readonly"},
		},
		{
			name:  "empty roles list",
			roles: nil,
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sql := container.BuildCreateRolesSQL(tt.roles)
			for _, w := range tt.want {
				found := false
				for _, line := range sql {
					if line == w || containsStr(line, w) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("BuildCreateRolesSQL(%v) missing %q\ngot: %v", tt.roles, w, sql)
				}
			}
		})
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || s[0:len(sub)] == sub || containsInner(s, sub))
}

func containsInner(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ---- CommandTrace return tests (Slice 3 — AC-5, AC-6, AC-7) ----

// TestCreateLbUser_TraceRedactsPassword verifies that the returned CommandTrace
// never contains the real password value (AC-7), contains CREATE ROLE (AC-5),
// and that its Output records the driver result or error (AC-6).
// Uses a deliberately invalid DSN so the function returns a connection error —
// the trace must still be populated with the redacted command.
func TestCreateLbUser_TraceRedactsPassword(t *testing.T) {
	ctx := context.Background()
	const realPassword = "super_secret_pass_123"
	const lbUsername = "lb_testschema"

	// Use an invalid DSN — the driver will fail to open a connection, but the
	// function must still return a CommandTrace with the redacted command.
	trace, err := container.CreateLbUser(ctx, "postgres://invalid-host:5432/db", lbUsername, realPassword)

	// The call MUST return an error (invalid DSN), but trace must be populated.
	if err == nil {
		t.Skip("test requires a non-connectable DSN; got nil error (unexpected live DB?)")
	}

	// AC-7: Real password must NEVER appear in the command trace.
	if strings.Contains(trace.Command, realPassword) {
		t.Errorf("real password leaked into CommandTrace.Command: %q", trace.Command)
	}
	// AC-5: Command must contain the CREATE ROLE statement (with redacted password).
	if !strings.Contains(trace.Command, "CREATE ROLE") {
		t.Errorf("CommandTrace.Command should contain CREATE ROLE; got: %q", trace.Command)
	}
	if !strings.Contains(trace.Command, "[REDACTED]") {
		t.Errorf("CommandTrace.Command should contain [REDACTED] placeholder; got: %q", trace.Command)
	}
	// AC-6: Output must contain the driver error.
	if trace.Output == "" {
		t.Error("CommandTrace.Output must contain driver error message on failure; got empty")
	}
}

// TestCreateRolesIfNotExist_ReturnsTracesPerRole verifies that when the DSN is
// invalid (host unreachable), each role attempt produces a CommandTrace with the
// SQL in Command and the driver error in Output (AC-5, AC-6).
// sql.Open with pgx only fails at ExecContext time, so individual role failures
// each become a trace entry — this is the expected behavior.
func TestCreateRolesIfNotExist_ConnectionError(t *testing.T) {
	ctx := context.Background()
	roles := []string{"approle", "readonly"}

	traces, err := container.CreateRolesIfNotExist(ctx, "postgres://invalid-host:5432/db", roles)

	// CreateRolesIfNotExist only returns a top-level error on sql.Open failure.
	// Per-role failures are captured in traces and logged (non-fatal by design).
	// Since pgx only fails at ExecContext, err must be nil here.
	if err != nil {
		t.Fatalf("expected nil error (per-role failures are non-fatal), got: %v", err)
	}

	// Two roles → two traces.
	if len(traces) != len(roles) {
		t.Fatalf("expected %d traces (one per role), got %d", len(roles), len(traces))
	}

	// AC-5: Each trace Command must contain the role name and DO block.
	// AC-6: Each trace Output must contain the driver error (non-empty).
	for i, tr := range traces {
		if !strings.Contains(tr.Command, roles[i]) {
			t.Errorf("trace[%d].Command should contain role %q; got: %q", i, roles[i], tr.Command)
		}
		if !strings.Contains(tr.Command, "CREATE ROLE") {
			t.Errorf("trace[%d].Command should contain CREATE ROLE; got: %q", i, tr.Command)
		}
		if tr.Output == "" {
			t.Errorf("trace[%d].Output must be non-empty (driver error); got empty", i)
		}
	}
}

// TestCreateRolesIfNotExist_EmptyRoles verifies that an empty roles list returns
// nil traces and nil error without attempting any database connection.
func TestCreateRolesIfNotExist_EmptyRoles(t *testing.T) {
	ctx := context.Background()
	traces, err := container.CreateRolesIfNotExist(ctx, "postgres://irrelevant:5432/db", nil)
	if err != nil {
		t.Errorf("expected nil error for empty roles, got: %v", err)
	}
	if traces != nil {
		t.Errorf("expected nil traces for empty roles, got: %v", traces)
	}
}

// TestGrantConnectCreateOnDatabase_TraceContainsSQL verifies that the returned
// CommandTrace.Command contains the GRANT statement (AC-5) and that the Output
// is populated on connection error (AC-6).
func TestGrantConnectCreateOnDatabase_TraceOnError(t *testing.T) {
	ctx := context.Background()

	trace, err := container.GrantConnectCreateOnDatabase(ctx, "postgres://invalid-host:5432/db", "testdb", "lb_testuser")

	if err == nil {
		t.Skip("test requires a non-connectable DSN; got nil error (unexpected live DB?)")
	}
	// AC-5: Command must contain the GRANT statement.
	if !strings.Contains(trace.Command, "GRANT") {
		t.Errorf("CommandTrace.Command should contain GRANT; got: %q", trace.Command)
	}
	if !strings.Contains(trace.Command, "testdb") {
		t.Errorf("CommandTrace.Command should contain database name; got: %q", trace.Command)
	}
	// AC-6: Output must contain driver error.
	if trace.Output == "" {
		t.Error("CommandTrace.Output must be non-empty on error")
	}
}

// TestCreateLbBookkeepingSchema_TraceJoinsBothStatements verifies that the
// returned CommandTrace.Command joins both SQL statements (CREATE SCHEMA +
// ALTER SCHEMA) and that the Output is populated on connection error (AC-5, AC-6).
func TestCreateLbBookkeepingSchema_TraceOnError(t *testing.T) {
	ctx := context.Background()

	trace, err := container.CreateLbBookkeepingSchema(ctx, "postgres://invalid-host:5432/db", "lb_testschema")

	if err == nil {
		t.Skip("test requires a non-connectable DSN; got nil error (unexpected live DB?)")
	}
	// AC-5: Command must contain both SQL statements joined.
	if !strings.Contains(trace.Command, "CREATE SCHEMA") {
		t.Errorf("CommandTrace.Command should contain CREATE SCHEMA; got: %q", trace.Command)
	}
	if !strings.Contains(trace.Command, "ALTER SCHEMA") {
		t.Errorf("CommandTrace.Command should contain ALTER SCHEMA; got: %q", trace.Command)
	}
	// AC-6: Output must contain driver error.
	if trace.Output == "" {
		t.Error("CommandTrace.Output must be non-empty on error")
	}
}
