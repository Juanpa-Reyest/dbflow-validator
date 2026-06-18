// Package logging provides a dual-sink slog setup for dbflow-validator.
//
// Console sink: respects the configured --log-level threshold.
// File sink: always records at DEBUG level, regardless of console level.
// Both sinks reuse the same redaction contract as the rest of the tool —
// callers must never pass raw secrets into log attributes.
package logging

import (
	"context"
	"io"
	"log/slog"
)

// multiHandler fans out slog records to two underlying handlers.
// The console handler is filtered at consoleLevel; the file handler always
// records at DEBUG (so every log event lands in the file unconditionally).
type multiHandler struct {
	console slog.Handler
	file    slog.Handler
}

// Enabled reports whether either handler would process a record at level.
// We always return true so that callers build the full record; each handler
// then applies its own level gate in Handle.
func (m *multiHandler) Enabled(_ context.Context, level slog.Level) bool {
	// Enable if either handler would accept the record.
	return m.console.Enabled(context.Background(), level) ||
		m.file.Enabled(context.Background(), level)
}

// Handle dispatches a log record to both handlers independently.
// Each handler applies its own level filter via its own Enabled check
// (the text/JSON handler implementations call Enabled internally).
func (m *multiHandler) Handle(ctx context.Context, r slog.Record) error {
	// Dispatch to console only if the console handler would accept it.
	if m.console.Enabled(ctx, r.Level) {
		if err := m.console.Handle(ctx, r); err != nil {
			return err
		}
	}
	// File always records (DEBUG threshold means all levels pass).
	if m.file.Enabled(ctx, r.Level) {
		if err := m.file.Handle(ctx, r); err != nil {
			return err
		}
	}
	return nil
}

// WithAttrs returns a new multiHandler with attrs applied to both children.
func (m *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &multiHandler{
		console: m.console.WithAttrs(attrs),
		file:    m.file.WithAttrs(attrs),
	}
}

// WithGroup returns a new multiHandler with the group applied to both children.
func (m *multiHandler) WithGroup(name string) slog.Handler {
	return &multiHandler{
		console: m.console.WithGroup(name),
		file:    m.file.WithGroup(name),
	}
}

// NewDualSink returns a *slog.Logger backed by a fan-out handler:
//   - consoleW receives records at consoleLevel and above (text format).
//   - fileW receives ALL records at DEBUG and above (readable enterprise format:
//     [YYYY-MM-DD HH:MM:SS]  LEVEL  ▸ message    key=val).
//
// Callers must never pass raw secret values as log attributes. The logging
// package trusts that all attribute values have already been redacted by the
// caller (domain.Secret.String() returns "***"; URLs use redactURL).
func NewDualSink(consoleW, fileW io.Writer, consoleLevel slog.Level) *slog.Logger {
	consoleHandler := slog.NewTextHandler(consoleW, &slog.HandlerOptions{
		Level: consoleLevel,
	})
	// File sink uses the enterprise readable format (not raw slog logfmt).
	// Always records DEBUG and above so every event lands in execution.log.
	fileHandler := NewReadableHandler(fileW, slog.LevelDebug)
	return slog.New(&multiHandler{
		console: consoleHandler,
		file:    fileHandler,
	})
}

// NewFileSink returns a *slog.Logger that writes ALL records (DEBUG and above)
// to fileW using the enterprise readable format. The console receives nothing —
// this is the intended production logger so that only the clean progress lines
// (emitted directly via fmt) appear on the console, not raw slog logfmt.
//
// Use this in main.go instead of NewDualSink when the console must stay quiet.
func NewFileSink(fileW io.Writer) *slog.Logger {
	return slog.New(NewReadableHandler(fileW, slog.LevelDebug))
}

// MavenWriter returns an io.Writer that routes Maven container stdout/stderr
// to both the console sink and the file sink verbatim.
//
// When fileW is nil (degraded mode — run dir creation failed), only consoleW
// receives the output. This allows Maven output to be streamed live even when
// the log file cannot be opened.
func MavenWriter(consoleW, fileW io.Writer) io.Writer {
	if fileW == nil {
		return consoleW
	}
	return io.MultiWriter(consoleW, fileW)
}
