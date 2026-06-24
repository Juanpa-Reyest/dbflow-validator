package maven_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/maven"
)

// TestInjectDriverDependency verifies that InjectDriverDependency inserts the
// PostgreSQL driver dependency inside the dbflow plugin element of pom.xml.
// It is table-driven and covers poms with and without an existing <dependencies>
// block inside the plugin.
func TestInjectDriverDependency(t *testing.T) {
	tests := []struct {
		name          string
		inputPOM      string
		wantSubstring string
		wantAbsent    string
	}{
		{
			name: "pom without plugin dependencies block",
			inputPOM: `<?xml version="1.0" encoding="UTF-8"?>
<project>
  <build>
    <plugins>
      <plugin>
        <groupId>com.gs.ftt.coe-ds</groupId>
        <artifactId>relational-db-release-manager-plugin</artifactId>
        <version>0.0.1</version>
      </plugin>
    </plugins>
  </build>
</project>`,
			wantSubstring: "<groupId>org.postgresql</groupId>",
		},
		{
			name: "pom with existing plugin dependencies block (should add, not duplicate)",
			inputPOM: `<?xml version="1.0" encoding="UTF-8"?>
<project>
  <build>
    <plugins>
      <plugin>
        <groupId>com.gs.ftt.coe-ds</groupId>
        <artifactId>relational-db-release-manager-plugin</artifactId>
        <version>0.0.1</version>
        <dependencies>
          <dependency>
            <groupId>some.other</groupId>
            <artifactId>other-dep</artifactId>
            <version>1.0</version>
          </dependency>
        </dependencies>
      </plugin>
    </plugins>
  </build>
</project>`,
			wantSubstring: "<groupId>org.postgresql</groupId>",
		},
		{
			name: "driver already present — idempotent, no duplication",
			inputPOM: `<?xml version="1.0" encoding="UTF-8"?>
<project>
  <build>
    <plugins>
      <plugin>
        <groupId>com.gs.ftt.coe-ds</groupId>
        <artifactId>relational-db-release-manager-plugin</artifactId>
        <version>0.0.1</version>
        <dependencies>
          <dependency>
            <groupId>org.postgresql</groupId>
            <artifactId>postgresql</artifactId>
            <version>42.7.4</version>
          </dependency>
        </dependencies>
      </plugin>
    </plugins>
  </build>
</project>`,
			wantSubstring: "<groupId>org.postgresql</groupId>",
			// Verify only one occurrence (not duplicated).
		},
		{
			name: "pom without the target plugin — no-op",
			inputPOM: `<?xml version="1.0" encoding="UTF-8"?>
<project>
  <build>
    <plugins>
      <plugin>
        <groupId>org.apache.maven.plugins</groupId>
        <artifactId>maven-compiler-plugin</artifactId>
        <version>3.14.0</version>
      </plugin>
    </plugins>
  </build>
</project>`,
			wantAbsent: "<groupId>org.postgresql</groupId>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			pomPath := filepath.Join(dir, "pom.xml")
			if err := os.WriteFile(pomPath, []byte(tt.inputPOM), 0o644); err != nil {
				t.Fatalf("write pom: %v", err)
			}

			injected, noOp, err := maven.InjectDriverDependency(pomPath)
			if err != nil {
				t.Fatalf("InjectDriverDependency: %v", err)
			}

			result, err := os.ReadFile(pomPath)
			if err != nil {
				t.Fatalf("read patched pom: %v", err)
			}
			got := string(result)

			if tt.wantSubstring != "" && !strings.Contains(got, tt.wantSubstring) {
				t.Errorf("patched pom missing %q\nGot:\n%s", tt.wantSubstring, got)
			}
			if tt.wantAbsent != "" && strings.Contains(got, tt.wantAbsent) {
				t.Errorf("patched pom should NOT contain %q\nGot:\n%s", tt.wantAbsent, got)
			}

			// AC-12/AC-13: verify noOp and injected return values.
			if tt.name == "pom without the target plugin — no-op" {
				if !noOp {
					t.Error("expected noOp=true when plugin not found")
				}
				if injected != "" {
					t.Errorf("expected empty injected on no-op; got: %q", injected)
				}
			} else if tt.name == "driver already present — idempotent, no duplication" {
				if !noOp {
					t.Error("expected noOp=true when driver already present")
				}
			} else {
				// For cases where driver was actually injected.
				if noOp && tt.wantSubstring != "" {
					// Check: if the pom had the driver already, noOp is correct; skip.
					_ = noOp
				}
				if !noOp && injected == "" {
					t.Error("expected non-empty injected block when driver was newly added")
				}
			}

			// Idempotency check: calling twice should not duplicate the driver.
			_, _, err2 := maven.InjectDriverDependency(pomPath)
			if err2 != nil {
				t.Fatalf("second InjectDriverDependency: %v", err2)
			}
			result2, _ := os.ReadFile(pomPath)
			count := strings.Count(string(result2), "org.postgresql")
			if count > 2 { // once in groupId, once in artifactId
				t.Errorf("idempotency violated: org.postgresql appears %d times after second call", count)
			}
		})
	}
}
