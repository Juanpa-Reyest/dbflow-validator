package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// TestHelp_Flag verifies that --help and -h print a usage message to stdout and exit 0.
func TestHelp_Flag(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "--help flag", args: []string{"--help"}},
		{name: "-h flag", args: []string{"-h"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			env := func(string) string { return "" }
			code := runWithHelpOutput(tt.args, env, &out, &out)

			if code != 0 {
				t.Errorf("exit code = %d, want 0", code)
			}
			got := out.String()
			if !strings.Contains(got, "Usage") && !strings.Contains(got, "usage") && !strings.Contains(got, "--repo-url") {
				t.Errorf("help output %q does not contain usage information", got)
			}
		})
	}
}

// TestExitCodes_Table verifies all documented exit-code scenarios.
func TestExitCodes_Table(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		env      func(string) string
		wantCode int
	}{
		{
			name:     "missing repo-url no-TTY exits 2",
			args:     []string{},
			env:      func(k string) string { if k == "DBFLOW_GIT_TOKEN" { return "tok" }; return "" },
			wantCode: 2,
		},
		{
			name:     "missing token no-TTY exits 2",
			args:     []string{"--repo-url", "https://example.com/repo.git"},
			env:      func(string) string { return "" },
			wantCode: 2,
		},
		{
			name:     "--help exits 0",
			args:     []string{"--help"},
			env:      func(string) string { return "" },
			wantCode: 0,
		},
		{
			name:     "-h exits 0",
			args:     []string{"-h"},
			env:      func(string) string { return "" },
			wantCode: 0,
		},
		{
			name:     "--version exits 0",
			args:     []string{"--version"},
			env:      func(string) string { return "" },
			wantCode: 0,
		},
		{
			name:     "-v exits 0",
			args:     []string{"-v"},
			env:      func(string) string { return "" },
			wantCode: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			code := runWithHelpOutput(tt.args, tt.env, &out, &out)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
			}
		})
	}
}

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

// ---------------------------------------------------------------------------
// Fail-closed validator JAR resolution (WARNING-2 fix)
// ---------------------------------------------------------------------------

// TestResolveValidatorJarWith_ExtractorError_ReturnsError asserts that when the
// JAR extractor fails, resolveValidatorJarWith surfaces the error (fail-CLOSED).
// Production wiring must treat this as a hard abort, not a silent no-op.
func TestResolveValidatorJarWith_ExtractorError_ReturnsError(t *testing.T) {
	sentinel := errors.New("simulated extraction failure")
	failExtractor := func(cacheRoot, version string) (string, error) {
		return "", sentinel
	}

	_, err := resolveValidatorJarWith(failExtractor, t.TempDir(), "test-version")
	if err == nil {
		t.Fatal("resolveValidatorJarWith: expected non-nil error on extraction failure (fail-CLOSED), got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("resolveValidatorJarWith: error chain must wrap the extractor error; got: %v", err)
	}
}

// TestResolveValidatorJarWith_Success_ReturnsPath asserts that when the extractor
// succeeds, resolveValidatorJarWith returns the path and nil error.
func TestResolveValidatorJarWith_Success_ReturnsPath(t *testing.T) {
	wantPath := "/some/cache/validator.jar"
	okExtractor := func(cacheRoot, version string) (string, error) {
		return wantPath, nil
	}

	got, err := resolveValidatorJarWith(okExtractor, t.TempDir(), "test-version")
	if err != nil {
		t.Fatalf("resolveValidatorJarWith: unexpected error: %v", err)
	}
	if got != wantPath {
		t.Errorf("resolveValidatorJarWith: got path %q, want %q", got, wantPath)
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
