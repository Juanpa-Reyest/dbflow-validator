package config

import (
	"regexp"
	"strings"
	"unicode"
)

// ansiEscape matches ANSI/VT100 escape sequences of the forms:
//   - CSI sequences: ESC [ <params> <final-byte>  (e.g. \x1b[C, \x1b[1;32m)
//   - SS3 sequences: ESC O <letter>               (e.g. \x1bOA for arrow keys in application mode)
//   - Any remaining lone ESC followed by a non-printable or bracket character
var ansiEscape = regexp.MustCompile(`\x1b(?:\[[0-9;]*[A-Za-z]|O[A-Za-z]|[^[\x1b])`)

// sanitizeRepoURL strips ANSI escape sequences, then trims surrounding
// whitespace and non-printable control characters from a repository URL
// read from interactive input.
//
// This prevents stray terminal escape codes (e.g. the right-arrow key \x1b[C
// captured by the readline buffer) from corrupting the URL passed to git.
func sanitizeRepoURL(raw string) string {
	cleaned := ansiEscape.ReplaceAllString(raw, "")
	return strings.TrimFunc(cleaned, func(r rune) bool {
		return unicode.IsSpace(r) || (r < 0x20 && r != '\t') || r == 0x7f
	})
}

// sanitizeToken trims surrounding whitespace and non-printable control
// characters from a token string. Interior characters are NOT altered —
// tokens may legitimately contain hyphens, underscores, and mixed case
// that must not be disturbed.
func sanitizeToken(raw string) string {
	return strings.TrimFunc(raw, func(r rune) bool {
		return unicode.IsSpace(r) || (r < 0x20) || r == 0x7f
	})
}
