package giturl_test

import (
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/giturl"
)

func TestIsSSHURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		// scp-style SSH URLs (user@host:path — no scheme)
		{name: "scp-style github", url: "git@github.com:org/repo.git", want: true},
		{name: "scp-style gitlab", url: "git@gitlab.com:org/repo.git", want: true},
		{name: "scp-style bitbucket", url: "git@bitbucket.org:org/repo.git", want: true},
		{name: "scp-style without .git suffix", url: "git@github.com:org/repo", want: true},
		{name: "scp-style nested path", url: "git@github.com:org/subgroup/repo.git", want: true},
		{name: "scp-style custom user", url: "admin@git.internal.company.com:org/repo.git", want: true},

		// ssh:// scheme URLs
		{name: "ssh:// github with user", url: "ssh://git@github.com/org/repo.git", want: true},
		{name: "ssh:// without .git suffix", url: "ssh://git@github.com/org/repo", want: true},
		{name: "ssh:// with port", url: "ssh://git@github.com:22/org/repo.git", want: true},
		{name: "ssh:// without user", url: "ssh://github.com/org/repo.git", want: true},

		// HTTPS URLs — must return false
		{name: "https github", url: "https://github.com/org/repo.git", want: false},
		{name: "https without .git", url: "https://github.com/org/repo", want: false},
		{name: "https with token", url: "https://x-access-token:tok@github.com/org/repo.git", want: false},
		{name: "http (insecure)", url: "http://github.com/org/repo.git", want: false},

		// Edge cases
		{name: "empty string", url: "", want: false},
		{name: "plain hostname only", url: "github.com", want: false},
		{name: "git:// protocol", url: "git://github.com/org/repo.git", want: false},
		{name: "file:// protocol", url: "file:///home/user/repo.git", want: false},
		{name: "local path", url: "/home/user/repo.git", want: false},
		{name: "relative path", url: "../repo.git", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := giturl.IsSSHURL(tt.url)
			if got != tt.want {
				t.Errorf("IsSSHURL(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}
