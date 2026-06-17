package liquibase

import (
	"bufio"
	"bytes"
	"strings"
)

// Properties holds the ordered list of lines from a liquibase.properties file.
// We store raw lines so that comments, blank lines, and key order are preserved
// (lossless round-trip). The Set/Get helpers operate on key=value lines only.
type Properties struct {
	// lines holds every raw line in document order (including comments/blanks).
	lines []propLine
}

type propLine struct {
	raw     string // original text
	key     string // non-empty only for key=value lines
	isEntry bool   // true when this line is a key=value entry
}

// Parse reads a Java .properties byte slice into a Properties value.
// Comments (#/!), blank lines, and ordering are all preserved.
func Parse(data []byte) (Properties, error) {
	var p Properties
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		raw := scanner.Text()
		trimmed := strings.TrimSpace(raw)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "!") {
			p.lines = append(p.lines, propLine{raw: raw})
			continue
		}

		idx := strings.IndexByte(trimmed, '=')
		if idx < 0 {
			// Not a key=value line — keep as raw.
			p.lines = append(p.lines, propLine{raw: raw})
			continue
		}

		key := strings.TrimSpace(trimmed[:idx])
		p.lines = append(p.lines, propLine{
			raw:     raw,
			key:     key,
			isEntry: true,
		})
	}
	return p, scanner.Err()
}

// Get returns the value for key, or "" if not found.
func (p *Properties) Get(key string) string {
	for _, l := range p.lines {
		if l.isEntry && l.key == key {
			idx := strings.IndexByte(l.raw, '=')
			if idx >= 0 {
				return strings.TrimSpace(l.raw[idx+1:])
			}
		}
	}
	return ""
}

// Set updates the value for key in-place. If the key does not exist, a new
// line is appended at the end.
func (p *Properties) Set(key, value string) {
	for i, l := range p.lines {
		if l.isEntry && l.key == key {
			p.lines[i].raw = key + "=" + value
			return
		}
	}
	// Key not found — append.
	p.lines = append(p.lines, propLine{
		raw:     key + "=" + value,
		key:     key,
		isEntry: true,
	})
}

// Render serialises Properties back to bytes. This is a pure function: it
// produces the same output for the same input, preserving all lines including
// comments and blanks.
func Render(p Properties) []byte {
	var b bytes.Buffer
	for _, l := range p.lines {
		b.WriteString(l.raw)
		b.WriteByte('\n')
	}
	return b.Bytes()
}
