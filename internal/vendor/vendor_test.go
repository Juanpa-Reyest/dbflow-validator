// Package vendor_test tests the Maven vendor settings.xml generation and repo path resolution.
package vendor_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/vendor"
)

// TestFindVendorRepository checks that FindVendorRepository returns the mvn-vendor/repository
// path when it exists relative to the given project root.
func TestFindVendorRepository(t *testing.T) {
	// Build a fake project root with the expected layout.
	root := t.TempDir()
	repoDir := filepath.Join(root, "mvn-vendor", "repository")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Touch a file to make it non-empty.
	pluginDir := filepath.Join(repoDir, "com", "gs", "ftt", "coe-ds",
		"relational-db-release-manager-plugin", "0.0.1")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatalf("mkdir plugin: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(pluginDir, "relational-db-release-manager-plugin-0.0.1.jar"),
		[]byte("fake"), 0o644,
	); err != nil {
		t.Fatalf("write plugin jar: %v", err)
	}

	got, err := vendor.FindVendorRepository(root)
	if err != nil {
		t.Fatalf("FindVendorRepository: %v", err)
	}
	if filepath.ToSlash(got) != filepath.ToSlash(repoDir) {
		t.Errorf("got %q, want %q", got, repoDir)
	}
}

func TestFindVendorRepository_NotFound(t *testing.T) {
	root := t.TempDir()
	// No mvn-vendor/repository.
	_, err := vendor.FindVendorRepository(root)
	if err == nil {
		t.Error("expected error when mvn-vendor/repository not found, got nil")
	}
}

func TestGenerateSettingsXML(t *testing.T) {
	repoPath := "/some/project/mvn-vendor/repository"
	xml, err := vendor.GenerateSettingsXML(repoPath)
	if err != nil {
		t.Fatalf("GenerateSettingsXML: %v", err)
	}

	wantSubstrings := []string{
		"<localRepository>" + repoPath + "</localRepository>",
		"vendor-local",
		"file://" + repoPath,
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(xml, want) {
			t.Errorf("GenerateSettingsXML output missing %q\nGot:\n%s", want, xml)
		}
	}
}

func TestWriteSettingsXML(t *testing.T) {
	dir := t.TempDir()
	repoPath := "/project/mvn-vendor/repository"

	settingsPath, err := vendor.WriteSettingsXML(dir, repoPath)
	if err != nil {
		t.Fatalf("WriteSettingsXML: %v", err)
	}
	if _, err := os.Stat(settingsPath); err != nil {
		t.Fatalf("settings.xml not found at %q: %v", settingsPath, err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings.xml: %v", err)
	}
	if !strings.Contains(string(data), repoPath) {
		t.Errorf("settings.xml does not contain repo path %q", repoPath)
	}
}
