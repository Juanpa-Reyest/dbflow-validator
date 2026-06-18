package orchestrator

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// MoveWorkspace moves the src directory to dst using os.Rename (atomic on the
// same filesystem). On EXDEV (cross-device / cross-filesystem) it falls back to
// a recursive copy followed by os.RemoveAll(src).
//
// This is used to move the ephemeral clone into <run>/workspace/ when the
// validation run ends with StatusFailed or when KeepWorkspace is true.
func MoveWorkspace(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	} else if !isCrossDevice(err) {
		return err
	}
	// Cross-device: fall back to recursive copy then remove.
	if err := copyDir(src, dst); err != nil {
		return err
	}
	return os.RemoveAll(src)
}

// isCrossDevice reports whether err is an EXDEV (cross-device link) error.
// This occurs when os.Rename is called across filesystem boundaries.
func isCrossDevice(err error) bool {
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		return errors.Is(linkErr.Err, syscall.EXDEV)
	}
	return false
}

// copyDir recursively copies src into dst.
// dst is created (MkdirAll) if it does not exist.
// Only regular files are copied; symlinks and device files are skipped.
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}

		if !d.Type().IsRegular() {
			// Skip symlinks, device files, etc.
			return nil
		}

		return copyFile(path, target)
	})
}

// copyFile copies a single regular file from src to dst, preserving permissions.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
