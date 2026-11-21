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
		m.emit(m.stream.Flush())
		m.emit(lead + diagnosticredact.Record(m.redactor, start[:end]))
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
	if len(m.pending) > maxJSONRecordBytes {
		m.maskPending()
		return true
	}
	return false
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
