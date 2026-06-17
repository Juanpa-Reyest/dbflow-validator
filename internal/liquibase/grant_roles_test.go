package liquibase_test

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/liquibase"
)

func TestExtractGrantTargetRoles(t *testing.T) {
	tests := []struct {
		name      string
		sql       string
		wantRoles []string
	}{
		{
			name: "single GRANT TO single role",
			sql:  "GRANT usage ON SCHEMA scgolfcore TO appbackend;",
			wantRoles: []string{"appbackend"},
		},
		{
			name: "multiple GRANT statements",
			sql: `GRANT usage ON SCHEMA scgolfcore TO appbackend;
GRANT EXECUTE ON all functions IN SCHEMA scgolfcore TO appbackend;
GRANT SELECT ON ALL TABLES IN SCHEMA scgolfcore TO readonly;`,
			wantRoles: []string{"appbackend", "readonly"},
		},
		{
			name: "GRANT TO multiple roles in one statement",
			sql:  "GRANT SELECT ON TABLE foo TO role1, role2;",
			wantRoles: []string{"role1", "role2"},
		},
		{
			name: "PUBLIC pseudo-role is excluded",
			sql:  "GRANT SELECT ON TABLE foo TO PUBLIC;",
			wantRoles: nil,
		},
		{
			name: "mixed PUBLIC and real roles",
			sql:  "GRANT SELECT ON TABLE foo TO PUBLIC, appbackend;",
			wantRoles: []string{"appbackend"},
		},
		{
			name: "ALTER DEFAULT PRIVILEGES GRANT is extracted",
			sql:  "ALTER DEFAULT privileges IN SCHEMA scgolfcore GRANT EXECUTE ON functions TO appbackend;",
			wantRoles: []string{"appbackend"},
		},
		{
			name: "no GRANT statements",
			sql:  "CREATE SCHEMA IF NOT EXISTS scgolfcore;",
			wantRoles: nil,
		},
		{
			// DML files can contain large HTML/text blobs with "TO activate",
			// "pay TO", "access TO", etc. The regex MUST require GRANT context
			// so those false positives are not treated as GRANT targets.
			name:      "DML string with TO in non-GRANT context is ignored",
			sql:       `INSERT INTO templates VALUES (1, 'To activate your account, you need TO confirm.');`,
			wantRoles: nil,
		},
		{
			name:      "partition clause FOR VALUES FROM/TO is ignored",
			sql:       `CREATE TABLE foo PARTITION OF bar FOR VALUES FROM ('2026-01-01') TO ('2026-02-01');`,
			wantRoles: nil,
		},
		{
			name: "scgolfcore DCL fixture (representative)",
			sql: `-- =====================================================
CREATE SCHEMA IF NOT EXISTS scgolfcore;
GRANT usage ON SCHEMA scgolfcore TO appbackend;
GRANT EXECUTE ON all functions IN SCHEMA scgolfcore TO appbackend;
GRANT EXECUTE ON all procedures IN SCHEMA scgolfcore TO appbackend;
ALTER DEFAULT privileges IN SCHEMA scgolfcore GRANT EXECUTE ON functions TO appbackend;`,
			wantRoles: []string{"appbackend"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := liquibase.ExtractGrantTargetRoles(tt.sql)
			sort.Strings(got)
			sort.Strings(tt.wantRoles)

			if len(got) != len(tt.wantRoles) {
				t.Errorf("got roles %v, want %v", got, tt.wantRoles)
				return
			}
			for i := range got {
				if got[i] != tt.wantRoles[i] {
					t.Errorf("got roles %v, want %v", got, tt.wantRoles)
					return
				}
			}
		})
	}
}

// TestExtractGrantTargetRolesFromDir tests the directory-scanning variant.
func TestExtractGrantTargetRolesFromDir(t *testing.T) {
	root := t.TempDir()
	sqlDir := filepath.Join(root, "src", "main", "resources", "db", "schema", "changelog", "DDL", "210", "sql")
	if err := os.MkdirAll(sqlDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	dcl := `CREATE SCHEMA IF NOT EXISTS scgolfcore;
GRANT usage ON SCHEMA scgolfcore TO appbackend;
GRANT EXECUTE ON all functions IN SCHEMA scgolfcore TO appbackend;
GRANT EXECUTE ON all procedures IN SCHEMA scgolfcore TO appbackend;
ALTER DEFAULT privileges IN SCHEMA scgolfcore GRANT EXECUTE ON functions TO appbackend;`

	if err := os.WriteFile(filepath.Join(sqlDir, "N0001_TA_CARGA_INICIAL_DCL.sql"), []byte(dcl), 0o644); err != nil {
		t.Fatalf("write sql: %v", err)
	}

	roles, err := liquibase.ExtractGrantTargetRolesFromArchetype(root)
	if err != nil {
		t.Fatalf("ExtractGrantTargetRolesFromArchetype: %v", err)
	}

	if len(roles) != 1 || roles[0] != "appbackend" {
		t.Errorf("got %v, want [appbackend]", roles)
	}
}
