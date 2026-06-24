package domain

import "strings"

// ScrubSecrets replaces every occurrence of each secret in s with "[REDACTED]".
// Empty secret values are silently ignored so callers can pass potentially-empty
// fields (e.g. an empty token) without conditional guards.
// Replacement is case-sensitive and uses strings.ReplaceAll semantics.
func ScrubSecrets(s string, secrets ...string) string {
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		s = strings.ReplaceAll(s, secret, "[REDACTED]")
	}
	return s
}
