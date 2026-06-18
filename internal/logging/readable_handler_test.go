package logging_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/logging"
)

// TestReadableHandler_FormatHasTimestamp verifies the [YYYY-MM-DD HH:MM:SS] prefix.
func TestReadableHandler_FormatHasTimestamp(t *testing.T) {
	var buf bytes.Buffer
	h := logging.NewReadableHandler(&buf, slog.LevelDebug)
	logger := slog.New(h)

	logger.Info("hello readable")

	out := buf.String()
	// Must start with [ and contain a date like 2026-
	if !strings.Contains(out, "[20") {
		t.Errorf("readable handler output must contain timestamp [20XX-...]; got %q", out)
	}
	// Must have closing ] from the timestamp bracket
	if !strings.Contains(out, "]") {
		t.Errorf("readable handler output must contain ] after timestamp; got %q", out)
	}
}

// TestReadableHandler_FormatHasLevel verifies the LEVEL token appears.
func TestReadableHandler_FormatHasLevel(t *testing.T) {
	var buf bytes.Buffer
	h := logging.NewReadableHandler(&buf, slog.LevelDebug)
	logger := slog.New(h)

	logger.Info("msg info")
	logger.Warn("msg warn")
	logger.Error("msg error")
	logger.Debug("msg debug")

	out := buf.String()
	for _, level := range []string{"INFO", "WARN", "ERROR", "DEBUG"} {
		if !strings.Contains(out, level) {
			t.Errorf("readable handler output missing level %q; got %q", level, out)
		}
	}
}

// TestReadableHandler_FormatHasArrow verifies ▸ separator between level and message.
func TestReadableHandler_FormatHasArrow(t *testing.T) {
	var buf bytes.Buffer
	h := logging.NewReadableHandler(&buf, slog.LevelDebug)
	logger := slog.New(h)

	logger.Info("arrow test")

	out := buf.String()
	if !strings.Contains(out, "▸") {
		t.Errorf("readable handler output must contain ▸ arrow; got %q", out)
	}
}

// TestReadableHandler_MessagePresent verifies the message text appears.
func TestReadableHandler_MessagePresent(t *testing.T) {
	var buf bytes.Buffer
	h := logging.NewReadableHandler(&buf, slog.LevelDebug)
	logger := slog.New(h)

	logger.Info("my test message")

	if !strings.Contains(buf.String(), "my test message") {
		t.Errorf("readable handler output missing message; got %q", buf.String())
	}
}

// TestReadableHandler_AttrsPresent verifies key=value attrs appear in output.
func TestReadableHandler_AttrsPresent(t *testing.T) {
	var buf bytes.Buffer
	h := logging.NewReadableHandler(&buf, slog.LevelDebug)
	logger := slog.New(h)

	logger.Info("step done", "step", "preflight", "duration_ms", 16)

	out := buf.String()
	if !strings.Contains(out, "preflight") {
		t.Errorf("readable handler must include attr value %q; got %q", "preflight", out)
	}
}

// TestReadableHandler_LevelFilterWorks verifies messages below the threshold are dropped.
func TestReadableHandler_LevelFilterWorks(t *testing.T) {
	var buf bytes.Buffer
	// Info level — DEBUG should be dropped
	h := logging.NewReadableHandler(&buf, slog.LevelInfo)
	logger := slog.New(h)

	logger.Debug("should not appear")
	logger.Info("should appear")

	out := buf.String()
	if strings.Contains(out, "should not appear") {
		t.Errorf("readable handler must filter DEBUG when level is INFO; got %q", out)
	}
	if !strings.Contains(out, "should appear") {
		t.Errorf("readable handler must include INFO message; got %q", out)
	}
}

// TestReadableHandler_NoSlogLogfmt verifies raw slog logfmt tokens do not appear.
func TestReadableHandler_NoSlogLogfmt(t *testing.T) {
	var buf bytes.Buffer
	h := logging.NewReadableHandler(&buf, slog.LevelDebug)
	logger := slog.New(h)

	logger.Info("test message", "key", "val")

	out := buf.String()
	// slog logfmt produces: time=... level=... msg=...
	// Our format must NOT produce these tokens.
	for _, bad := range []string{"time=", "level=", "msg="} {
		if strings.Contains(out, bad) {
			t.Errorf("readable handler must NOT produce slog logfmt token %q; got %q", bad, out)
		}
	}
}

// TestNewDualSink_FileUsesReadableFormat verifies the NEW dual sink uses readable format for file.
func TestNewDualSink_FileUsesReadableFormat(t *testing.T) {
	var consoleBuf, fileBuf bytes.Buffer
	logger := logging.NewDualSink(&consoleBuf, &fileBuf, slog.LevelInfo)

	logger.Info("file format test")

	fileOut := fileBuf.String()
	// File sink must use readable format (not raw logfmt).
	if strings.Contains(fileOut, "time=") {
		t.Errorf("file sink must NOT use raw slog logfmt 'time='; got %q", fileOut)
	}
	if strings.Contains(fileOut, "level=") {
		t.Errorf("file sink must NOT use raw slog logfmt 'level='; got %q", fileOut)
	}
	if !strings.Contains(fileOut, "▸") {
		t.Errorf("file sink must use readable format with ▸ arrow; got %q", fileOut)
	}
}
