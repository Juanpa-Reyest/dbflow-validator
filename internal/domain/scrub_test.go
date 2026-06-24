package domain_test

import (
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
)

func TestScrubSecrets(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		secrets []string
		want    string
	}{
		{
			name:    "no secrets: input returned unchanged",
			input:   "git clone https://github.com/org/repo.git /tmp/dest",
			secrets: nil,
			want:    "git clone https://github.com/org/repo.git /tmp/dest",
		},
		{
			name:    "single secret: replaced with [REDACTED]",
			input:   "Cloning into x-access-token:abc123@github.com/org/repo.git",
			secrets: []string{"abc123"},
			want:    "Cloning into x-access-token:[REDACTED]@github.com/org/repo.git",
		},
		{
			name:    "multiple occurrences of same secret: all replaced",
			input:   "token=abc123 url=https://abc123@host password=abc123",
			secrets: []string{"abc123"},
			want:    "token=[REDACTED] url=https://[REDACTED]@host password=[REDACTED]",
		},
		{
			name:    "multiple distinct secrets: all replaced",
			input:   "user tok1 pass tok2 again tok1",
			secrets: []string{"tok1", "tok2"},
			want:    "user [REDACTED] pass [REDACTED] again [REDACTED]",
		},
		{
			name:    "empty secret entry: ignored (not replaced)",
			input:   "some text with spaces",
			secrets: []string{"", "tok"},
			want:    "some text with spaces",
		},
		{
			name:    "all secrets empty: input returned unchanged",
			input:   "some text",
			secrets: []string{"", ""},
			want:    "some text",
		},
		{
			name:    "token URL pattern: x-access-token:<token>@ is redacted",
			input:   "git clone https://x-access-token:ghp_secretToken123@github.com/org/repo.git /dest",
			secrets: []string{"ghp_secretToken123"},
			want:    "git clone https://x-access-token:[REDACTED]@github.com/org/repo.git /dest",
		},
		{
			name:    "password pattern in connection string",
			input:   "host=127.0.0.1 user=lb_app password=supersecretpass dbname=db",
			secrets: []string{"supersecretpass"},
			want:    "host=127.0.0.1 user=lb_app password=[REDACTED] dbname=db",
		},
		{
			name:    "secret not present: input returned unchanged",
			input:   "nothing sensitive here",
			secrets: []string{"absent-secret"},
			want:    "nothing sensitive here",
		},
		{
			name:    "empty input string: returns empty string",
			input:   "",
			secrets: []string{"tok"},
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := domain.ScrubSecrets(tt.input, tt.secrets...)
			if got != tt.want {
				t.Errorf("ScrubSecrets(%q, %v) =\n  got:  %q\n  want: %q", tt.input, tt.secrets, got, tt.want)
			}
		})
	}
}
