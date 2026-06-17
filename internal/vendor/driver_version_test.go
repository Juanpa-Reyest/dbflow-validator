package vendor_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/maven"
)

// TestVendoredDriverJarExists asserts that the PostgreSQL JDBC driver JAR
// is present in the embedded vendor repository at the path derived from
// maven.PostgresDriverVersion.
//
// This test is the single-source-of-truth guard: if the version constant is
// bumped in maven.PostgresDriverVersion or the JAR is renamed/moved, this
// test will fail immediately, preventing a silent desync between the injected
// pom version and the vendored artifact.
func TestVendoredDriverJarExists(t *testing.T) {
	// Locate the project root relative to this test file.
	// The test file lives at internal/vendor/driver_version_test.go,
	// so the project root is two directories up.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	// thisFile: .../internal/vendor/driver_version_test.go
	// projectRoot: .../
	projectRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")

	version := maven.PostgresDriverVersion
	if version == "" {
		t.Fatal("maven.PostgresDriverVersion is empty")
	}

	// Expected path: <projectRoot>/mvn-vendor/repository/org/postgresql/postgresql/<version>/postgresql-<version>.jar
	jarPath := filepath.Join(
		projectRoot,
		"mvn-vendor", "repository",
		"org", "postgresql", "postgresql",
		version,
		"postgresql-"+version+".jar",
	)

	if _, err := os.Stat(jarPath); err != nil {
		t.Errorf(
			"vendored PostgreSQL JDBC driver not found at %q (maven.PostgresDriverVersion=%q): %v\n"+
				"A version bump without updating the vendored JAR will cause offline Maven resolution to fail.",
			jarPath, version, err,
		)
	}
}
