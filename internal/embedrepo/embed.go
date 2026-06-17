// Package embedrepo embeds the vendored Maven repository into the binary and
// provides idempotent extraction to a per-version cache directory.
//
// The vendored repository lives at internal/embedrepo/mvn-vendor/repository
// and is compiled into the binary via //go:embed. On the first run the contents
// are extracted to:
//
//	~/.cache/dbflow-validator/<version>/m2/
//
// Subsequent runs reuse the cached extraction without re-extracting, as long as
// the sentinel file (.complete) is present and its checksum matches the embedded
// content hash.
//
// The Maven container mounts the extracted path at /m2 (read-only) for offline
// artifact resolution. No loose asset files are required alongside the binary.
package embedrepo

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Embedded vendored Maven repository tree.
// The "all:" prefix ensures files starting with '.' or '_' (e.g., _remote.repositories)
// are included, which Maven requires for proper artifact resolution.
//
//go:embed all:mvn-vendor/repository
var repoFS embed.FS

// repositoryRoot is the embedded path prefix to strip when extracting.
const repositoryRoot = "mvn-vendor/repository"

// CachePath returns the absolute path to the extracted repository for a given
// cache root and version string:
//
//	<cacheRoot>/<version>/m2
func CachePath(cacheRoot, version string) string {
	return filepath.Join(cacheRoot, version, "m2")
}

// EnsureExtracted extracts the embedded repository to:
//
//	<cacheRoot>/<version>/m2/
//
// Extraction is skipped when the sentinel file (<repoPath>/.complete) exists
// and its content matches the SHA-256 of the embedded content.
// Returns the absolute path to the extracted repository directory.
func EnsureExtracted(cacheRoot, version string) (string, error) {
	repoPath := CachePath(cacheRoot, version)
	sentinelPath := filepath.Join(repoPath, ".complete")

	// Compute the expected checksum of the embedded FS.
	expectedChecksum, err := embeddedChecksum()
	if err != nil {
		return "", fmt.Errorf("compute embedded checksum: %w", err)
	}

	// Check if sentinel exists and matches the expected checksum.
	if data, readErr := os.ReadFile(sentinelPath); readErr == nil {
		if strings.TrimSpace(string(data)) == expectedChecksum {
			// Already extracted and valid — no-op.
			return repoPath, nil
		}
	}

	// (Re-)extract the embedded repository.
	if err := os.MkdirAll(repoPath, 0o750); err != nil {
		return "", fmt.Errorf("create cache dir %q: %w", repoPath, err)
	}

	if err := extractFS(repoPath); err != nil {
		return "", fmt.Errorf("extract embedded repo to %q: %w", repoPath, err)
	}

	// Write the sentinel with the checksum.
	if err := os.WriteFile(sentinelPath, []byte(expectedChecksum), 0o600); err != nil {
		return "", fmt.Errorf("write sentinel %q: %w", sentinelPath, err)
	}

	return repoPath, nil
}

// embeddedChecksum computes a deterministic SHA-256 over all files in the
// embedded FS, sorted by path. The checksum changes when any file changes.
func embeddedChecksum() (string, error) {
	h := sha256.New()

	err := fs.WalkDir(repoFS, repositoryRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		// Hash the file path (for rename detection) and its content.
		h.Write([]byte(path))

		f, err := repoFS.Open(path)
		if err != nil {
			return fmt.Errorf("open embedded %q: %w", path, err)
		}
		defer f.Close()

		if _, err := io.Copy(h, f); err != nil {
			return fmt.Errorf("hash embedded %q: %w", path, err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// extractFS walks the embedded FS and writes every file to destDir,
// preserving the directory structure (with the repositoryRoot prefix stripped).
func extractFS(destDir string) error {
	return fs.WalkDir(repoFS, repositoryRoot, func(embPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Strip the repositoryRoot prefix to get the relative destination path.
		relPath, relErr := filepath.Rel(repositoryRoot, embPath)
		if relErr != nil {
			return fmt.Errorf("resolve relative path for %q: %w", embPath, relErr)
		}
		destPath := filepath.Join(destDir, relPath)

		if d.IsDir() {
			return os.MkdirAll(destPath, 0o750)
		}

		// Ensure parent directory exists (WalkDir visits dirs before files, but be safe).
		if err := os.MkdirAll(filepath.Dir(destPath), 0o750); err != nil {
			return fmt.Errorf("mkdir for %q: %w", destPath, err)
		}

		f, err := repoFS.Open(embPath)
		if err != nil {
			return fmt.Errorf("open embedded file %q: %w", embPath, err)
		}
		defer f.Close()

		out, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o640)
		if err != nil {
			return fmt.Errorf("create dest file %q: %w", destPath, err)
		}
		defer out.Close()

		if _, err := io.Copy(out, f); err != nil {
			return fmt.Errorf("write %q: %w", destPath, err)
		}
		return nil
	})
}
