// Package giturl provides URL scheme detection helpers for git repository URLs.
package giturl

import "strings"

// IsSSHURL reports whether url is an SSH repository URL.
//
// It returns true for two SSH forms:
//   - scp-style (user@host:path): e.g. git@github.com:org/repo.git
//   - ssh:// scheme: e.g. ssh://git@github.com/org/repo.git
//
// It returns false for HTTPS, HTTP, git://, file://, and local paths.
func IsSSHURL(url string) bool {
	if url == "" {
		return false
	}
	// ssh:// scheme — explicit SSH protocol.
	if strings.HasPrefix(url, "ssh://") {
		return true
	}
	// Reject any other known scheme before the scp-style check, so that
	// "https://", "http://", "git://", "file://" never match.
	if strings.Contains(url, "://") {
		return false
	}
	// Reject local absolute and relative paths (start with / or .).
	if strings.HasPrefix(url, "/") || strings.HasPrefix(url, ".") {
		return false
	}
	// scp-style: must contain "@" before ":" and ":" before "/", e.g. git@host:path.
	// The "@" separates the user from the host, and ":" separates the host from the path.
	atIdx := strings.Index(url, "@")
	if atIdx < 0 {
		return false
	}
	colonIdx := strings.Index(url, ":")
	// ":" must appear after "@" to form user@host:path.
	return colonIdx > atIdx
}
