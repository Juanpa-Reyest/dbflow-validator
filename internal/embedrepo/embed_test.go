package embedrepo_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/embedrepo"
)

// TestCachePath verifies the expected cache directory layout.
func TestCachePath(t *testing.T) {
	root := "/tmp/cache-root"
	got := embedrepo.CachePath(root, "1.2.3")
	want := filepath.Join(root, "1.2.3", "m2")
	if got != want {
		t.Errorf("CachePath() = %q, want %q", got, want)
	}
}

// TestEnsureExtracted_MissingDirectory_Extracts verifies that extraction runs
// when the cache directory does not exist.
func TestEnsureExtracted_MissingDirectory_Extracts(t *testing.T) {
	cacheRoot := t.TempDir()
	repoPath, err := embedrepo.EnsureExtracted(cacheRoot, "test-v0.0.1")
	if err != nil {
		t.Fatalf("EnsureExtracted() unexpected error: %v", err)
	}
	if repoPath == "" {
		t.Fatal("EnsureExtracted() returned empty path")
	}
	// The returned path must exist on disk.
	if _, err := os.Stat(repoPath); err != nil {
		t.Errorf("extracted path %q does not exist: %v", repoPath, err)
	}
	// The returned path must be the expected cache location.
	expected := embedrepo.CachePath(cacheRoot, "test-v0.0.1")
	if repoPath != expected {
		t.Errorf("repoPath = %q, want %q", repoPath, expected)
	}
}

// TestEnsureExtracted_Idempotent verifies that a second call is a no-op when
// the sentinel .complete file is already present with a matching checksum.
func TestEnsureExtracted_Idempotent(t *testing.T) {
	cacheRoot := t.TempDir()

	// First extraction.
	path1, err := embedrepo.EnsureExtracted(cacheRoot, "idempotent-test")
	if err != nil {
		t.Fatalf("first EnsureExtracted(): %v", err)
	}

	// Record sentinel mtime for the idempotency check.
	sentinelPath := filepath.Join(path1, ".complete")
	info1, err := os.Stat(sentinelPath)
	if err != nil {
		t.Fatalf("sentinel not found after first extract: %v", err)
	}
	mtime1 := info1.ModTime()

	// Second extraction — must be a no-op (sentinel already valid).
	path2, err := embedrepo.EnsureExtracted(cacheRoot, "idempotent-test")
	if err != nil {
		t.Fatalf("second EnsureExtracted(): %v", err)
	}

	info2, err := os.Stat(sentinelPath)
	if err != nil {
		t.Fatalf("sentinel not found after second extract: %v", err)
	}
	mtime2 := info2.ModTime()

	if mtime1 != mtime2 {
		t.Error("sentinel mtime changed on second call — extraction was not skipped (idempotency failed)")
	}
	if path1 != path2 {
		t.Errorf("paths differ: %q vs %q", path1, path2)
	}
}

// TestEnsureExtracted_SentinelCorrupt_Reextracts verifies that if the sentinel
// checksum does not match, extraction runs again.
func TestEnsureExtracted_SentinelCorrupt_Reextracts(t *testing.T) {
	cacheRoot := t.TempDir()

	// First extraction.
	repoPath, err := embedrepo.EnsureExtracted(cacheRoot, "corrupt-test")
	if err != nil {
		t.Fatalf("first EnsureExtracted(): %v", err)
	}

	// Corrupt the sentinel.
	sentinelPath := filepath.Join(repoPath, ".complete")
	if err := os.WriteFile(sentinelPath, []byte("bad-checksum"), 0o600); err != nil {
		t.Fatalf("corrupt sentinel: %v", err)
	}

	// Second extraction should re-extract (sentinel content is wrong).
	_, err = embedrepo.EnsureExtracted(cacheRoot, "corrupt-test")
	if err != nil {
		t.Fatalf("second EnsureExtracted() with corrupt sentinel: %v", err)
	}

	// Sentinel must be rewritten with valid checksum.
	data, err := os.ReadFile(sentinelPath)
	if err != nil {
		t.Fatalf("read sentinel: %v", err)
	}
	if strings.TrimSpace(string(data)) == "bad-checksum" {
		t.Error("sentinel still contains bad checksum after re-extraction")
	}
}

// TestEnsureExtracted_ContainsExpectedFiles verifies that the extracted cache
// contains the expected vendored artifacts.
func TestEnsureExtracted_ContainsExpectedFiles(t *testing.T) {
	cacheRoot := t.TempDir()
	repoPath, err := embedrepo.EnsureExtracted(cacheRoot, "content-test")
	if err != nil {
		t.Fatalf("EnsureExtracted(): %v", err)
	}

	// Check that at least one known artifact exists.
	pluginJAR := filepath.Join(repoPath,
		"com", "gs", "ftt", "coe-ds",
		"relational-db-release-manager-plugin", "0.0.1",
		"relational-db-release-manager-plugin-0.0.1.jar",
	)
	if _, err := os.Stat(pluginJAR); err != nil {
		t.Errorf("expected plugin JAR at %q: %v", pluginJAR, err)
	}
}
