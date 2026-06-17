package liquibase_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/liquibase"
)

func TestExtractSchemaName(t *testing.T) {
	tests := []struct {
		name     string
		sql      string
		want     string
		wantErr  bool
	}{
		{
			name: "plain CREATE SCHEMA",
			sql:  "CREATE SCHEMA scgolfcore;\n",
			want: "scgolfcore",
		},
		{
			name: "CREATE SCHEMA IF NOT EXISTS",
			sql:  "CREATE SCHEMA IF NOT EXISTS scgolfcore;\n",
			want: "scgolfcore",
		},
		{
			name: "uppercase keyword, mixed-case schema",
			sql:  "CREATE SCHEMA IF NOT EXISTS ScGolfCore;",
			want: "ScGolfCore",
		},
		{
			name: "lowercase keyword variant",
			sql:  "create schema if not exists mypayments;",
			want: "mypayments",
		},
		{
			name: "quoted identifier",
			sql:  `CREATE SCHEMA "my_schema";`,
			want: "my_schema",
		},
		{
			name: "schema in middle of SQL block",
			sql: `-- comment
CREATE TABLE foo (id INT);
CREATE SCHEMA IF NOT EXISTS analytics;
GRANT usage ON SCHEMA analytics TO app;`,
			want: "analytics",
		},
		{
			name:    "no CREATE SCHEMA statement",
			sql:     "CREATE TABLE foo (id INT);",
			wantErr: true,
		},
		{
			name:    "empty string",
			sql:     "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			sqlFile := filepath.Join(dir, "ddl.sql")
			if err := os.WriteFile(sqlFile, []byte(tt.sql), 0o644); err != nil {
				t.Fatalf("write sql: %v", err)
			}

			got, err := liquibase.ExtractSchemaName(sqlFile)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("ExtractSchemaName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLbUsername(t *testing.T) {
	tests := []struct {
		schema string
		want   string
	}{
		{schema: "scgolfcore", want: "lb_scgolfcore"},
		{schema: "analytics", want: "lb_analytics"},
		{schema: "MySchema", want: "lb_MySchema"},
	}
	for _, tt := range tests {
		got := liquibase.LbUsername(tt.schema)
		if got != tt.want {
			t.Errorf("LbUsername(%q) = %q, want %q", tt.schema, got, tt.want)
		}
	}
}

// TestExtractSchemaFromDir finds the DDL SQL file in a directory tree and
// extracts the schema name — this exercises the directory-scan helper.
func TestExtractSchemaFromDir(t *testing.T) {
	root := t.TempDir()
	ddlDir := filepath.Join(root, "src", "main", "resources", "db", "schema", "changelog", "DDL", "210", "sql")
	if err := os.MkdirAll(ddlDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Write a representative DCL sql file.
	dcl := `CREATE SCHEMA IF NOT EXISTS scgolfcore;
GRANT usage ON SCHEMA scgolfcore TO appbackend;
`
	if err := os.WriteFile(filepath.Join(ddlDir, "N0001_TA_CARGA_INICIAL_DCL.sql"), []byte(dcl), 0o644); err != nil {
		t.Fatalf("write sql: %v", err)
	}

	schema, err := liquibase.ExtractSchemaFromArchetype(root)
	if err != nil {
		t.Fatalf("ExtractSchemaFromArchetype: %v", err)
	}
	if schema != "scgolfcore" {
		t.Errorf("got %q, want %q", schema, "scgolfcore")
	}
}
