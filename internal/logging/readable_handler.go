package logging

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// readableHandler is a slog.Handler that formats records in the enterprise style:
//
//	[2006-01-02 15:04:05]  INFO  ▸ message    key=value key2=value2
//
// It replaces the raw slog logfmt output (time=... level=... msg=...) in the
// file sink so the live execution.log is human-readable during a run.
type readableHandler struct {
	mu      sync.Mutex
	w       io.Writer
	level   slog.Level
	preAttrs []slog.Attr
	groups  []string
}

// NewReadableHandler returns a slog.Handler that writes human-readable lines to w.
// Records below minLevel are dropped. Token redaction remains the caller's responsibility.
func NewReadableHandler(w io.Writer, minLevel slog.Level) slog.Handler {
	return &readableHandler{w: w, level: minLevel}
}

func (h *readableHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *readableHandler) Handle(_ context.Context, r slog.Record) error {
	var buf bytes.Buffer

	// [YYYY-MM-DD HH:MM:SS]
	ts := r.Time
	if ts.IsZero() {
		ts = time.Now()
	}
	fmt.Fprintf(&buf, "[%s]  ", ts.Format("2006-01-02 15:04:05"))

	// LEVEL
	lvl := levelLabel(r.Level)
	fmt.Fprintf(&buf, "%-5s  ▸ ", lvl)

	// Message
	buf.WriteString(r.Message)

	// Pre-set attrs (from WithAttrs)
	var attrBuf bytes.Buffer
	for _, a := range h.preAttrs {
		appendAttr(&attrBuf, a)
	}
	// Record attrs
	r.Attrs(func(a slog.Attr) bool {
		appendAttr(&attrBuf, a)
		return true
	})
	if attrBuf.Len() > 0 {
		buf.WriteString("    ")
		buf.Write(attrBuf.Bytes())
	}

	buf.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.w.Write(buf.Bytes())
	return err
}

func (h *readableHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newAttrs := make([]slog.Attr, len(h.preAttrs)+len(attrs))
	copy(newAttrs, h.preAttrs)
	copy(newAttrs[len(h.preAttrs):], attrs)
	return &readableHandler{
		w:        h.w,
		level:    h.level,
		preAttrs: newAttrs,
		groups:   h.groups,
	}
}

func (h *readableHandler) WithGroup(name string) slog.Handler {
	groups := make([]string, len(h.groups)+1)
	copy(groups, h.groups)
	groups[len(h.groups)] = name
	return &readableHandler{
		w:        h.w,
		level:    h.level,
		preAttrs: h.preAttrs,
		groups:   groups,
	}
}

// levelLabel returns an uppercase, fixed-width label for a slog.Level.
func levelLabel(level slog.Level) string {
	switch {
	case level >= slog.LevelError:
		return "ERROR"
	case level >= slog.LevelWarn:
		return "WARN"
	case level >= slog.LevelInfo:
		return "INFO"
	default:
		return "DEBUG"
	}
}

// appendAttr appends a key=value pair to buf.
func appendAttr(buf *bytes.Buffer, a slog.Attr) {
	if a.Equal(slog.Attr{}) {
		return
	}
	val := a.Value.Resolve()
	if buf.Len() > 0 {
		buf.WriteByte(' ')
	}
	buf.WriteString(a.Key)
	buf.WriteByte('=')
	switch val.Kind() {
	case slog.KindString:
		s := val.String()
		if strings.ContainsAny(s, " \t\n\"") {
			fmt.Fprintf(buf, "%q", s)
		} else {
			buf.WriteString(s)
		}
	default:
		buf.WriteString(val.String())
	}
}
