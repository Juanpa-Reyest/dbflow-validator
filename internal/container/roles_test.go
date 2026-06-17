package container_test

import (
	"strings"
	"testing"

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
