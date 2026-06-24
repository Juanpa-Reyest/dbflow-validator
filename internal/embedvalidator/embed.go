// Package embedvalidator embeds the vendored SQL rules validator JAR into the
// binary and provides idempotent extraction to a per-version cache directory.
//
// The JAR lives at internal/embedvalidator/jar/library-script-validator-postgresql.jar
// and is compiled into the binary via //go:embed.  On the first run it is extracted
// to:
//
//	~/.cache/dbflow-validator/<version>/validator/validator.jar
//
// Subsequent runs reuse the cached extraction without re-extracting, as long as
// the sentinel file (<cacheDir>/.complete) is present and its content matches
// the SHA-256 of the embedded JAR.
//
// The JAR is mounted read-only into the validator container at /val/validator.jar.
// No loose JAR file is required alongside the binary.
package embedvalidator

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// jarBytes holds the embedded validator JAR.
//
//go:embed jar/library-script-validator-postgresql.jar
var jarBytes []byte

// jarFileName is the fixed name for the extracted JAR inside the cache directory.
const jarFileName = "validator.jar"

// JARPath returns the absolute path of the extracted JAR for a given cache root
// and version string:
//
//	<cacheRoot>/<version>/validator/validator.jar
func JARPath(cacheRoot, version string) string {
	return filepath.Join(cacheRoot, version, "validator", jarFileName)
}

// EnsureExtracted extracts the embedded JAR to:
//
//	<cacheRoot>/<version>/validator/validator.jar
//
// Extraction is skipped when the sentinel file
// (<cacheRoot>/<version>/validator/.complete) exists and its content matches
// the SHA-256 of the embedded JAR.
// Returns the absolute path to the extracted JAR file.
func EnsureExtracted(cacheRoot, version string) (string, error) {
	cacheDir := filepath.Join(cacheRoot, version, "validator")
	jarPath := filepath.Join(cacheDir, jarFileName)
	sentinelPath := filepath.Join(cacheDir, ".complete")

	// Compute the expected checksum of the embedded JAR.
	expectedChecksum := embeddedChecksum()

	// Check if sentinel exists and matches.
	if data, readErr := os.ReadFile(sentinelPath); readErr == nil {
		if strings.TrimSpace(string(data)) == expectedChecksum {
			return jarPath, nil
		}
	}

	// (Re-)extract the embedded JAR.
	if err := os.MkdirAll(cacheDir, 0o750); err != nil {
		return "", fmt.Errorf("embedvalidator: create cache dir %q: %w", cacheDir, err)
	}

	out, err := os.OpenFile(jarPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o640)
	if err != nil {
		return "", fmt.Errorf("embedvalidator: create jar file %q: %w", jarPath, err)
	}

	if _, err := io.WriteString(out, ""); err != nil {
		out.Close()
		return "", fmt.Errorf("embedvalidator: init jar file: %w", err)
	}
	out.Close()

	if err := os.WriteFile(jarPath, jarBytes, 0o640); err != nil {
		return "", fmt.Errorf("embedvalidator: write jar %q: %w", jarPath, err)
	}

	// Write the sentinel with the checksum.
	if err := os.WriteFile(sentinelPath, []byte(expectedChecksum), 0o600); err != nil {
		return "", fmt.Errorf("embedvalidator: write sentinel %q: %w", sentinelPath, err)
	}

	return jarPath, nil
}

// embeddedChecksum computes the SHA-256 of the embedded JAR bytes.
func embeddedChecksum() string {
	h := sha256.New()
	h.Write(jarBytes)
	return hex.EncodeToString(h.Sum(nil))
}
