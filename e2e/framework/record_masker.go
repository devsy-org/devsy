package framework

import (
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/devsy-org/devsy/internal/diagnosticredact"
	"github.com/devsy-org/devsy/logging/redact"
)

const maxJSONRecordBytes = 64 << 10

type recordMasker struct {
	redactor *redact.Redactor
	stream   *redact.Stream
	emit     func(string)
	pending  string
	midLine  bool
	skip     *recordSkipper
}

// recordSkipper tracks JSON nesting so an oversized record can be discarded
// without buffering until its closing bracket arrives.
type recordSkipper struct {
	depth    int
	inString bool
	escaped  bool
}

// consume returns the text after the record's end and whether the record closed.
func (s *recordSkipper) consume(text string) (string, bool) {
	for i := 0; i < len(text); i++ {
		if s.advance(text[i]) {
			return text[i+1:], true
		}
	}
	return "", false
}

func (s *recordSkipper) advance(c byte) bool {
	switch {
	case s.escaped:
		s.escaped = false
	case s.inString:
		s.inString = c != '"'
		s.escaped = c == '\\'
	case c == '"':
		s.inString = true
	case c == '{' || c == '[':
		s.depth++
	case c == '}' || c == ']':
		s.depth--
		return s.depth <= 0
	}
	return false
}

func newRecordMasker(redactor *redact.Redactor, emit func(string)) *recordMasker {
	return &recordMasker{
		redactor: redactor,
		stream:   diagnosticredact.Stream(redactor),
		emit:     emit,
	}
}

func (m *recordMasker) write(text string) {
	m.pending += text
	m.drain(false)
}

func (m *recordMasker) finish() {
	m.drain(true)
	m.emit(m.stream.Flush())
}

func (m *recordMasker) drain(final bool) {
	for m.pending != "" && m.step(final) {
	}
}

func (m *recordMasker) step(final bool) bool {
	if m.skip != nil {
		rest, closed := m.skip.consume(m.pending)
		m.pending = rest
		if closed {
			m.skip = nil
		}
		return closed
	}
	if m.midLine {
		return m.copyLine()
	}
	start := strings.TrimLeft(m.pending, " \t")
	if start == "" {
		if final {
			return m.copyLine()
		}
		return false
	}
	if start[0] != '{' && start[0] != '[' {
		return m.copyLine()
	}
	return m.maskRecord(start, final)
}

func (m *recordMasker) copyLine() bool {
	end := strings.IndexAny(m.pending, "\r\n")
	if end < 0 {
		m.plain(m.pending)
		m.pending = ""
		m.midLine = true
		return false
	}
	end++
	if strings.HasPrefix(m.pending[end-1:], "\r\n") {
		end++
	}
	m.plain(m.pending[:end])
	m.pending = m.pending[end:]
	m.midLine = false
	return true
}

func (m *recordMasker) plain(text string) {
	m.emit(m.stream.RedactChunk(text))
}

func (m *recordMasker) maskRecord(start string, final bool) bool {
	if !final && !strings.ContainsAny(m.pending, "\r\n") {
		return m.holdOrOverflow()
	}
	var record json.RawMessage
	decoder := json.NewDecoder(strings.NewReader(start))
	err := decoder.Decode(&record)
	switch {
	case err == nil:
		lead := m.pending[:len(m.pending)-len(start)]
		end := int(decoder.InputOffset())
		m.plain(lead + diagnosticredact.Record(m.redactor, start[:end]))
		m.pending = start[end:]
		return true
	case errors.Is(err, io.ErrUnexpectedEOF):
		if final {
			m.maskPending()
			return true
		}
		return m.holdOrOverflow()
	default:
		return m.copyLine()
	}
}

func (m *recordMasker) holdOrOverflow() bool {
	if len(m.pending) <= maxJSONRecordBytes {
		return false
	}
	// Text that breaks JSON syntax within the limit is a plain line; skipping it
	// would drop everything up to a closing bracket that may never come. Bytes
	// past the limit must not decide this, or a malformed record's tail leaks.
	if !couldBeJSON(m.pending[:maxJSONRecordBytes]) {
		return m.copyLine()
	}
	skip := &recordSkipper{}
	rest, closed := skip.consume(m.pending)
	m.maskPending()
	if closed {
		m.pending = rest
	} else {
		m.skip = skip
	}
	return true
}

func couldBeJSON(text string) bool {
	var record json.RawMessage
	err := json.NewDecoder(strings.NewReader(text)).Decode(&record)
	return err == nil || errors.Is(err, io.ErrUnexpectedEOF)
}

func (m *recordMasker) maskPending() {
	m.pending = ""
	m.emit(m.stream.Flush())
	m.emit(m.redactor.Mask() + "\n")
}

func maskRecordText(redactor *redact.Redactor, value string) string {
	var out strings.Builder
	masker := newRecordMasker(redactor, func(text string) { out.WriteString(text) })
	masker.write(value)
	masker.finish()
	return out.String()
}
