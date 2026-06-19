package config

import (
	"testing"
)

// TestSanitizeRepoURL verifies that sanitizeRepoURL strips ANSI escape sequences,
// surrounding whitespace, and non-printable control characters from interactive input.
func TestSanitizeRepoURL(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "clean URL unchanged",
			input: "git@github.com:org/repo.git",
			want:  "git@github.com:org/repo.git",
		},
		{
			name:  "trailing ANSI right-arrow ESC[C stripped",
			input: "git@github.com:org/scgolfcore.git\x1b[C",
			want:  "git@github.com:org/scgolfcore.git",
		},
		{
			name:  "surrounding whitespace trimmed",
			input: "  git@github.com:org/repo.git  ",
			want:  "git@github.com:org/repo.git",
		},
		{
			name:  "trailing newline trimmed",
			input: "git@github.com:org/repo.git\n",
			want:  "git@github.com:org/repo.git",
		},
		{
			name:  "trailing CR trimmed",
			input: "git@github.com:org/repo.git\r",
			want:  "git@github.com:org/repo.git",
		},
		{
			name:  "ANSI sequence with numbers and letter stripped",
			input: "https://github.com/org/repo.git\x1b[1;32m",
			want:  "https://github.com/org/repo.git",
		},
		{
			name:  "multiple ANSI sequences stripped",
			input: "\x1b[0mhttps://github.com/org/repo.git\x1b[C\x1b[C",
			want:  "https://github.com/org/repo.git",
		},
		{
			name:  "naked ESC followed by non-bracket stripped",
			input: "git@github.com:org/repo.git\x1bO",
			want:  "git@github.com:org/repo.git",
		},
		{
			name:  "spaces plus ANSI combined",
			input: "  git@github.com:org/repo.git\x1b[C  ",
			want:  "git@github.com:org/repo.git",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeRepoURL(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeRepoURL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestSanitizeToken verifies that sanitizeToken trims only surrounding
// whitespace and control characters but does NOT alter interior characters.
func TestSanitizeToken(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "clean token unchanged",
			input: "ghp_abc123XYZ",
			want:  "ghp_abc123XYZ",
		},
		{
			name:  "leading/trailing whitespace trimmed",
			input: "  ghp_abc123XYZ  ",
			want:  "ghp_abc123XYZ",
		},
		{
			name:  "trailing newline trimmed",
			input: "ghp_abc123XYZ\n",
			want:  "ghp_abc123XYZ",
		},
		{
			name:  "interior characters NOT stripped",
			input: "ghp_abc 123",
			want:  "ghp_abc 123",
		},
		{
			name:  "trailing CR trimmed",
			input: "ghp_abc123XYZ\r",
			want:  "ghp_abc123XYZ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeToken(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeToken(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
