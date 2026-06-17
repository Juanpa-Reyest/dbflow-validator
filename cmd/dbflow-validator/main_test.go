package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun_MissingRepoURL_ExitsWithCode2(t *testing.T) {
	args := []string{} // no --repo-url
	env := func(k string) string {
		if k == "DBFLOW_GIT_TOKEN" {
			return "fake-token"
		}
		return ""
	}

	code := run(args, env)
	if code != 2 {
		t.Errorf("expected exit code 2 for missing --repo-url, got %d", code)
	}
}

func TestRun_MissingToken_ExitsWithCode2(t *testing.T) {
	args := []string{"--repo-url", "https://example.com/repo.git"}
	env := func(k string) string { return "" } // no token

	code := run(args, env)
	if code != 2 {
		t.Errorf("expected exit code 2 for missing token, got %d", code)
	}
}

// TestVersion_Flag verifies that --version and -v print the version string
// and exit with code 0 without attempting any network or Docker operations.
func TestVersion_Flag(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "--version flag", args: []string{"--version"}},
		{name: "-v flag", args: []string{"-v"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			env := func(string) string { return "" }
			code := runWithOutput(tt.args, env, &out)

			if code != 0 {
				t.Errorf("exit code = %d, want 0", code)
			}
			got := out.String()
			if !strings.Contains(got, "dbflow-validator") {
				t.Errorf("output %q does not contain 'dbflow-validator'", got)
			}
		})
	}
}
