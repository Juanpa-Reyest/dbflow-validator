// Package rundir provides helpers for per-run artifacts directory naming.
package rundir

import (
	"path/filepath"
	"time"
)

// timestampFormat is the human-readable local-time format used for run directory names.
// The format is sortable, filesystem-safe (no colons), and produces names like
// "2026-06-18_19-45-06" — no T separator, no Z UTC marker.
const timestampFormat = "2006-01-02_15-04-05"

// RunDirPath returns the path for a single run's artifacts directory.
// The result is <outputDir>/<timestamp> where timestamp follows "2006-01-02_15-04-05".
// The timestamp is rendered in local time (as displayed to the user), not UTC.
//
// outputDir is used as-is; the caller is responsible for resolving it to an
// absolute path when needed (e.g. relative to os.Getwd() in main.go).
func RunDirPath(outputDir string, ts time.Time) string {
	stamp := ts.Local().Format(timestampFormat)
	return filepath.Join(outputDir, stamp)
}
