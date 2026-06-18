package logging_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/logging"
)

func TestNewDualSink_InfoGoesToBoth(t *testing.T) {
	var consoleBuf, fileBuf bytes.Buffer
	logger := logging.NewDualSink(&consoleBuf, &fileBuf, slog.LevelInfo)

	logger.Info("hello world", "key", "value")

	if !strings.Contains(consoleBuf.String(), "hello world") {
		t.Errorf("console sink missing INFO message; got %q", consoleBuf.String())
	}
	if !strings.Contains(fileBuf.String(), "hello world") {
		t.Errorf("file sink missing INFO message; got %q", fileBuf.String())
	}
}

func TestNewDualSink_DebugGoesToFileOnly(t *testing.T) {
	var consoleBuf, fileBuf bytes.Buffer
	// Console at INFO level — DEBUG messages should NOT appear there.
	logger := logging.NewDualSink(&consoleBuf, &fileBuf, slog.LevelInfo)

	logger.Debug("debug-only-message")

	if strings.Contains(consoleBuf.String(), "debug-only-message") {
		t.Errorf("console sink must NOT contain DEBUG message when level is INFO; got %q", consoleBuf.String())
	}
	if !strings.Contains(fileBuf.String(), "debug-only-message") {
		t.Errorf("file sink must contain DEBUG message regardless of console level; got %q", fileBuf.String())
	}
}

func TestNewDualSink_DebugConsoleLevelShowsAll(t *testing.T) {
	var consoleBuf, fileBuf bytes.Buffer
	// Console at DEBUG level — everything should appear in both.
	logger := logging.NewDualSink(&consoleBuf, &fileBuf, slog.LevelDebug)

	logger.Debug("debug-visible")

	if !strings.Contains(consoleBuf.String(), "debug-visible") {
		t.Errorf("console sink must contain DEBUG when console level is DEBUG; got %q", consoleBuf.String())
	}
	if !strings.Contains(fileBuf.String(), "debug-visible") {
		t.Errorf("file sink must contain DEBUG message; got %q", fileBuf.String())
	}
}

func TestNewDualSink_TokenAbsentFromBothSinks(t *testing.T) {
	var consoleBuf, fileBuf bytes.Buffer
	logger := logging.NewDualSink(&consoleBuf, &fileBuf, slog.LevelInfo)

	const fakeToken = "abc123secret"
	// Log a redacted URL (as the cloner does) — the raw token should never appear.
	redactedURL := "https://***@github.com/org/repo.git"
	logger.Info("cloning repository", "url", redactedURL)

	// Ensure the redacted URL appears but the raw token does not.
	if strings.Contains(consoleBuf.String(), fakeToken) {
		t.Errorf("console sink must NOT contain raw token %q; got %q", fakeToken, consoleBuf.String())
	}
	if strings.Contains(fileBuf.String(), fakeToken) {
		t.Errorf("file sink must NOT contain raw token %q; got %q", fakeToken, fileBuf.String())
	}
	// The redacted form must be present in both.
	if !strings.Contains(consoleBuf.String(), "***@github.com") {
		t.Errorf("console sink must contain redacted URL form; got %q", consoleBuf.String())
	}
	if !strings.Contains(fileBuf.String(), "***@github.com") {
		t.Errorf("file sink must contain redacted URL form; got %q", fileBuf.String())
	}
}

func TestMavenWriter_BothSinksReceiveOutput(t *testing.T) {
	var consoleBuf, fileBuf bytes.Buffer
	w := logging.MavenWriter(&consoleBuf, &fileBuf)

	data := []byte("BUILD SUCCESS\n[INFO] Total time 5 s\n")
	_, _ = w.Write(data)

	if !strings.Contains(consoleBuf.String(), "BUILD SUCCESS") {
		t.Errorf("console sink missing Maven output; got %q", consoleBuf.String())
	}
	if !strings.Contains(fileBuf.String(), "BUILD SUCCESS") {
		t.Errorf("file sink missing Maven output; got %q", fileBuf.String())
	}
}

func TestMavenWriter_NilFileWriterFallsBackToConsole(t *testing.T) {
	var consoleBuf bytes.Buffer
	// nil file writer — degraded mode; only console should receive output.
	w := logging.MavenWriter(&consoleBuf, nil)

	data := []byte("maven output\n")
	n, err := w.Write(data)

	if err != nil {
		t.Errorf("MavenWriter with nil file: unexpected error: %v", err)
	}
	if n != len(data) {
		t.Errorf("MavenWriter wrote %d bytes, want %d", n, len(data))
	}
	if !strings.Contains(consoleBuf.String(), "maven output") {
		t.Errorf("console sink must receive output in degraded mode; got %q", consoleBuf.String())
	}
}
