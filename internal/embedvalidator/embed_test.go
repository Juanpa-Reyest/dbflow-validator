package embedvalidator_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/embedvalidator"
)

func TestJARPath_ReturnsNonEmpty(t *testing.T) {
	cacheRoot := t.TempDir()
	jarPath, err := embedvalidator.EnsureExtracted(cacheRoot, "test-v0.0.1")
	if err != nil {
		t.Fatalf("EnsureExtracted() unexpected error: %v", err)
	}
	if jarPath == "" {
		t.Fatal("EnsureExtracted() returned empty jarPath")
	}
}

func TestEnsureExtracted_FileExists(t *testing.T) {
	cacheRoot := t.TempDir()
	jarPath, err := embedvalidator.EnsureExtracted(cacheRoot, "test-v0.0.1")
	if err != nil {
		t.Fatalf("EnsureExtracted() error: %v", err)
	}
	if _, statErr := os.Stat(jarPath); statErr != nil {
		t.Errorf("JAR file at %q does not exist: %v", jarPath, statErr)
	}
}

func TestEnsureExtracted_PathContainsVersion(t *testing.T) {
	cacheRoot := t.TempDir()
	version := "v1.2.3"
	jarPath, err := embedvalidator.EnsureExtracted(cacheRoot, version)
	if err != nil {
		t.Fatalf("EnsureExtracted() error: %v", err)
	}
	if !strings.Contains(jarPath, version) {
		t.Errorf("jarPath %q does not contain version %q", jarPath, version)
	}
}

func TestEnsureExtracted_Idempotent(t *testing.T) {
	cacheRoot := t.TempDir()

	path1, err := embedvalidator.EnsureExtracted(cacheRoot, "idempotent-test")
	if err != nil {
		t.Fatalf("first EnsureExtracted() error: %v", err)
	}

	// Get mtime of sentinel before second call.
	sentinelPath := filepath.Join(filepath.Dir(path1), ".complete")
	info1, err := os.Stat(sentinelPath)
	if err != nil {
		t.Fatalf("sentinel not found: %v", err)
	}
	mtime1 := info1.ModTime()

	path2, err := embedvalidator.EnsureExtracted(cacheRoot, "idempotent-test")
	if err != nil {
		t.Fatalf("second EnsureExtracted() error: %v", err)
	}

	info2, err := os.Stat(sentinelPath)
	if err != nil {
		t.Fatalf("sentinel not found after second call: %v", err)
	}
	mtime2 := info2.ModTime()

	if mtime1 != mtime2 {
		t.Error("sentinel mtime changed on second call — not idempotent")
	}
	if path1 != path2 {
		t.Errorf("paths differ: %q vs %q", path1, path2)
	}
}

func TestEnsureExtracted_SentinelCorrupt_Reextracts(t *testing.T) {
	cacheRoot := t.TempDir()
	jarPath, err := embedvalidator.EnsureExtracted(cacheRoot, "corrupt-test")
	if err != nil {
		t.Fatalf("first EnsureExtracted() error: %v", err)
	}

	// Corrupt the sentinel.
	sentinelPath := filepath.Join(filepath.Dir(jarPath), ".complete")
	if err := os.WriteFile(sentinelPath, []byte("bad-checksum"), 0o600); err != nil {
		t.Fatalf("corrupt sentinel: %v", err)
	}

	_, err = embedvalidator.EnsureExtracted(cacheRoot, "corrupt-test")
	if err != nil {
		t.Fatalf("second EnsureExtracted() with corrupt sentinel: %v", err)
	}

	data, err := os.ReadFile(sentinelPath)
	if err != nil {
		t.Fatalf("read sentinel: %v", err)
	}
	if strings.TrimSpace(string(data)) == "bad-checksum" {
		t.Error("sentinel still contains bad checksum after re-extraction")
	}
}
