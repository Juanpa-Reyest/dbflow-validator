// Package rundir provides helpers for per-run artifacts directory naming.
package rundir

import (
	"path/filepath"
	"time"
)

// timestampFormat is the ISO-8601 compact UTC format used for run directory names.
// The format is sortable, filesystem-safe (no colons), and unambiguous.
const timestampFormat = "20060102T150405Z"

// RunDirPath returns the path for a single run's artifacts directory.
// The result is <outputDir>/<timestamp> where timestamp follows "20060102T150405Z".
//
// outputDir is used as-is; the caller is responsible for resolving it to an
// absolute path when needed (e.g. relative to os.Getwd() in main.go).
func RunDirPath(outputDir string, ts time.Time) string {
	stamp := ts.UTC().Format(timestampFormat)
	return filepath.Join(outputDir, stamp)
}
