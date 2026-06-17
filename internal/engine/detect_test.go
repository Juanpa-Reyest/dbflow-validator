package engine_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
	"github.com/dbflow-validator/dbflow-validator/internal/engine"
)

// writeProps creates a liquibase.properties file in a temp dir and returns its path.
func writeProps(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "liquibase.properties")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write props: %v", err)
	}
	return path
}

func TestDetect(t *testing.T) {
	tests := []struct {
		name        string
		props       string
		wantEngine  engine.Engine
		wantErr     bool
		errContains string
	}{
		{
			name: "postgres url + postgres driver -> Postgres",
			props: "url=jdbc:postgresql://localhost:5432/mydb\n" +
				"driver=org.postgresql.Driver\n",
			wantEngine: engine.EnginePostgres,
		},
		{
			name: "oracle url -> ErrUnsupportedEngine (Oracle)",
			props: "url=jdbc:oracle:thin:@localhost:1521:XE\n" +
				"driver=oracle.jdbc.OracleDriver\n",
			wantErr:     true,
			errContains: "Oracle",
		},
		{
			name: "snowflake url -> ErrUnsupportedEngine (Snowflake, cloud-only)",
			props: "url=jdbc:snowflake://account.snowflakecomputing.com\n" +
				"driver=net.snowflake.client.jdbc.SnowflakeDriver\n",
			wantErr:     true,
			errContains: "Snowflake",
		},
		{
			name: "placeholder url + postgres driver -> Postgres (multi-engine scaffold pattern)",
			props: "url=jdbc:db://placeholder:0000/db\n" +
				"driver=org.postgresql.Driver\n",
			wantEngine: engine.EnginePostgres,
		},
		{
			name: "oracle url + postgres driver -> HARD REJECT (ambiguous, never guess)",
			props: "url=jdbc:oracle:thin:@localhost:1521:XE\n" +
				"driver=org.postgresql.Driver\n",
			wantErr:     true,
			errContains: "ambiguous",
		},
		{
			name: "unknown url + unknown driver -> ErrUnsupportedEngine",
			props: "url=jdbc:db2://localhost:50000/sample\n" +
				"driver=com.ibm.db2.jcc.DB2Driver\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeProps(t, tt.props)

			got, err := engine.Detect(path)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Detect() expected error, got nil (engine=%v)", got)
				}
				if !errors.Is(err, domain.ErrUnsupportedEngine) {
					t.Errorf("Detect() error = %v, want wrapping %v", err, domain.ErrUnsupportedEngine)
				}
				if tt.errContains != "" && !containsStr(err.Error(), tt.errContains) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("Detect() unexpected error: %v", err)
			}
			if got != tt.wantEngine {
				t.Errorf("Detect() = %v, want %v", got, tt.wantEngine)
			}
		})
	}
}

func TestDetect_FileAbsent(t *testing.T) {
	_, err := engine.Detect("/nonexistent/path/liquibase.properties")
	if err == nil {
		t.Fatal("Detect() expected error for missing file, got nil")
	}
}

func TestProviderFor(t *testing.T) {
	t.Run("Postgres returns non-nil provider", func(t *testing.T) {
		p, err := engine.ProviderFor(engine.EnginePostgres)
		if err != nil {
			t.Fatalf("ProviderFor(Postgres) unexpected error: %v", err)
		}
		if p == nil {
			t.Fatal("ProviderFor(Postgres) returned nil provider")
		}
	})

	t.Run("unsupported engine returns error", func(t *testing.T) {
		_, err := engine.ProviderFor(engine.EngineOracle)
		if err == nil {
			t.Fatal("ProviderFor(Oracle) expected error, got nil")
		}
	})
}

func containsStr(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
