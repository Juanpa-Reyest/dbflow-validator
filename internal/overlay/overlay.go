// Package overlay implements the domain.Overlayer port.
// It copies the developer's local SQLInput tree into the freshly-cloned
// repository's SQLInput directory before sync, using clear-then-copy semantics.
package overlay

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
)

// Overlay implements domain.Overlayer.
type Overlay struct{}

// New returns a new Overlay.
func New() *Overlay {
	return &Overlay{}
}

// Apply clears destSQLInputDir, then recursively copies all regular .sql files
// from srcDir into destSQLInputDir, preserving subdirectory hierarchy.
//
// Rules:
//   - Only regular files with a .sql extension are copied (no symlinks, no devices).
//   - Directories in the destination are created with 0700 permissions.
//   - Files are written with 0600 permissions.
//   - The source directory is never mutated.
//   - Returns ErrNoPendingSQL (wrapped) when srcDir contains no .sql files.
func (o *Overlay) Apply(srcDir, destSQLInputDir string) (copied int, err error) {
	// First pass: count .sql files in source to allow fail-fast.
	sqlCount, err := countSQLFiles(srcDir)
	if err != nil {
		return 0, fmt.Errorf("overlay: scan source %s: %w", srcDir, err)
	}
	if sqlCount == 0 {
		return 0, fmt.Errorf("overlay: %s: %w", srcDir, domain.ErrNoPendingSQL)
	}

	// Clear destination contents (but keep the directory itself).
	if err := clearDir(destSQLInputDir); err != nil {
		return 0, fmt.Errorf("overlay: clear dest %s: %w", destSQLInputDir, err)
	}

	// Second pass: copy .sql files preserving subdirectory hierarchy.
	n, err := copySQLTree(srcDir, destSQLInputDir)
	if err != nil {
		return n, fmt.Errorf("overlay: copy from %s to %s: %w", srcDir, destSQLInputDir, err)
	}
	return n, nil
}

// countSQLFiles returns the number of regular .sql files in dir (recursive).
func countSQLFiles(dir string) (int, error) {
	count := 0
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(d.Name()), ".sql") {
			count++
		}
		return nil
	})
	return count, err
}

// clearDir removes the contents of dir without removing dir itself.
func clearDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		// Destination doesn't exist yet — nothing to clear, create it.
		return os.MkdirAll(dir, 0o700)
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// copySQLTree recursively copies all regular .sql files from src into dest,
// mirroring the subdirectory structure. Returns the number of files copied.
func copySQLTree(src, dest string) (int, error) {
	copied := 0
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil // directories are created on demand when copying files
		}
		if !d.Type().IsRegular() {
			return nil // skip symlinks and device files
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ".sql") {
			return nil // only copy .sql files
		}

		// Compute destination path preserving relative subdirectory.
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		destPath := filepath.Join(dest, rel)

		// Ensure the parent directory exists.
		if err := os.MkdirAll(filepath.Dir(destPath), 0o700); err != nil {
			return fmt.Errorf("create dest dir: %w", err)
		}

		// Copy the file.
		if err := copyFile(path, destPath); err != nil {
			return fmt.Errorf("copy %s → %s: %w", path, destPath, err)
		}
		copied++
		return nil
	})
	return copied, err
}

// copyFile copies the contents of src to dest with 0600 permissions.
func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// Ensure Overlay satisfies domain.Overlayer at compile time.
var _ domain.Overlayer = (*Overlay)(nil)
