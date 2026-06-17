package main

import (
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
