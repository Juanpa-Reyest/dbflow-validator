// Package vendor locates the embedded Maven vendor repository and generates
// a settings.xml that directs Maven to resolve the dbflow plugin and
// the PostgreSQL JDBC driver from that local path, with no network downloads.
//
// The Maven vendor repository lives at <project-root>/mvn-vendor/repository
// and must contain:
//   - com/gs/ftt/coe-ds/relational-db-release-manager-plugin/0.0.1/
//   - org/postgresql/postgresql/42.7.4/
package vendor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// VendorDir is the directory name used for the embedded Maven repo relative to the project root.
	VendorDir = "mvn-vendor"
	// RepositorySubdir is the Maven repository layout directory inside VendorDir.
	RepositorySubdir = "repository"
)

// FindVendorRepository walks from projectRoot to locate <projectRoot>/mvn-vendor/repository.
// Returns the absolute path to the repository directory.
// Returns an error if the directory does not exist.
func FindVendorRepository(projectRoot string) (string, error) {
	repoPath := filepath.Join(projectRoot, VendorDir, RepositorySubdir)
	if _, err := os.Stat(repoPath); err != nil {
		return "", fmt.Errorf(
			"mvn vendor repository not found at %q: %w (run the setup to install plugin and driver JARs)",
			repoPath, err,
		)
	}
	return repoPath, nil
}

// GenerateSettingsXML returns a Maven settings.xml string that:
//   - Sets localRepository to repoPath so Maven caches resolved artifacts there.
//   - Declares a "vendor-local" repository mirror for the embedded JARs.
//   - Keeps central available (for Maven core plugins like maven-compiler-plugin)
//     while redirecting plugin+driver resolution to the local repo.
//
// repoPath must be an absolute filesystem path to the repository directory.
func GenerateSettingsXML(repoPath string) (string, error) {
	if repoPath == "" {
		return "", fmt.Errorf("repoPath must not be empty")
	}
	// Ensure consistent slash format for the file:// URL.
	fileURL := "file://" + filepath.ToSlash(repoPath)

	// The settings use the localRepository override to prevent Maven from
	// downloading the vendored artifacts even when running with central access.
	// The vendor-local repository is declared both as a pluginRepository and
	// a dependency repository so that both the plugin and the PostgreSQL driver
	// resolve from the local filesystem without any network request.
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<settings xmlns="http://maven.apache.org/SETTINGS/1.0.0"
          xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
          xsi:schemaLocation="http://maven.apache.org/SETTINGS/1.0.0
                              https://maven.apache.org/xsd/settings-1.0.0.xsd">

  <!-- Override local repository so vendored artifacts are found first. -->
  <localRepository>`)
	sb.WriteString(repoPath)
	sb.WriteString(`</localRepository>

  <!-- offline=false allows Maven core plugins to be downloaded from central  -->
  <!-- when not already in the local repo; the vendored plugin + driver must  -->
  <!-- already be installed under localRepository (done by the setup step).   -->
  <offline>false</offline>

  <profiles>
    <profile>
      <id>vendor-local</id>
      <activation>
        <activeByDefault>true</activeByDefault>
      </activation>
      <repositories>
        <repository>
          <id>vendor-local</id>
          <url>`)
	sb.WriteString(fileURL)
	sb.WriteString(`</url>
          <releases><enabled>true</enabled></releases>
          <snapshots><enabled>false</enabled></snapshots>
        </repository>
      </repositories>
      <pluginRepositories>
        <pluginRepository>
          <id>vendor-local</id>
          <url>`)
	sb.WriteString(fileURL)
	sb.WriteString(`</url>
          <releases><enabled>true</enabled></releases>
          <snapshots><enabled>false</enabled></snapshots>
        </pluginRepository>
      </pluginRepositories>
    </profile>
  </profiles>

</settings>
`)
	return sb.String(), nil
}

// WriteSettingsXML generates a settings.xml for the given repoPath and writes
// it to dir/settings.xml. Returns the absolute path to the written file.
func WriteSettingsXML(dir, repoPath string) (string, error) {
	xml, err := GenerateSettingsXML(repoPath)
	if err != nil {
		return "", err
	}
	settingsPath := filepath.Join(dir, "settings.xml")
	if err := os.WriteFile(settingsPath, []byte(xml), 0o600); err != nil {
		return "", fmt.Errorf("write settings.xml to %q: %w", settingsPath, err)
	}
	return settingsPath, nil
}
