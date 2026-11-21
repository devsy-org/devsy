package framework

import (
	"encoding/json"
	"io"
	"strings"

	"github.com/devsy-org/devsy/internal/diagnosticredact"
	"github.com/devsy-org/devsy/logging/redact"
	"github.com/devsy-org/devsy/pkg/livetee"
)

const maxLiveJSONRecord = 64 << 10

type liveDiagnosticCapture struct {
	tee      *livetee.Tee
	redactor *redact.Redactor
	stream   *redact.Stream
	pending  string
	record   string
	midLine  bool
}

func newLiveDiagnosticCapture(tee *livetee.Tee, redactor *redact.Redactor) *liveDiagnosticCapture {
	return &liveDiagnosticCapture{
		tee:      tee,
		redactor: redactor,
		stream:   diagnosticredact.Stream(redactor),
	}
}

func (w *liveDiagnosticCapture) Write(p []byte) (int, error) {
	w.pending += string(p)
	w.drain(false)
	return len(p), nil
}

func (w *liveDiagnosticCapture) close() int64 {
	w.drain(true)
	if w.record != "" {
		w.maskRecord()
	}
	_, _ = io.WriteString(w.tee, w.stream.Flush())
	return w.tee.Close()
}

func (w *liveDiagnosticCapture) drain(final bool) {
	for w.pending != "" && w.step(final) {
	}
}

func (w *liveDiagnosticCapture) step(final bool) bool {
	newline := strings.IndexByte(w.pending, '\n')
	switch {
	case w.record != "":
		return w.stepRecord(newline)
	case w.midLine:
		return w.stepMidLine(newline)
	case newline < 0:
		return w.stepPartialStart(final)
	}
	line := w.consume(newline + 1)
	if opensJSONRecord(line) {
		w.record = line
		return true
	}
	w.passThrough(line)
	return true
}

func (w *liveDiagnosticCapture) stepRecord(newline int) bool {
	if newline >= 0 {
		w.extendRecord(w.consume(newline + 1))
		return true
	}
	if len(w.record)+len(w.pending) > maxLiveJSONRecord {
		w.pending = ""
		w.maskRecord()
	}
	return false
}

func (w *liveDiagnosticCapture) stepMidLine(newline int) bool {
	if newline < 0 {
		w.passThrough(w.consume(len(w.pending)))
		return false
	}
	w.passThrough(w.consume(newline + 1))
	w.midLine = false
	return true
}

func (w *liveDiagnosticCapture) stepPartialStart(final bool) bool {
	if !final && mayOpenJSONRecord(w.pending) {
		return false
	}
	w.midLine = true
	w.passThrough(w.consume(len(w.pending)))
	return true
}

func (w *liveDiagnosticCapture) consume(n int) string {
	text := w.pending[:n]
	w.pending = w.pending[n:]
	return text
}

func (w *liveDiagnosticCapture) passThrough(text string) {
	_, _ = io.WriteString(w.tee, w.stream.RedactChunk(text))
}

func (w *liveDiagnosticCapture) extendRecord(line string) {
	w.record += line
	body := strings.TrimRight(w.record, "\r\n")
	switch {
	case json.Valid([]byte(body)):
		_, _ = io.WriteString(w.tee, w.stream.Flush())
		_, _ = io.WriteString(w.tee, diagnosticredact.Record(w.redactor, body))
		_, _ = io.WriteString(w.tee, w.record[len(body):])
		w.record = ""
	case len(w.record) > maxLiveJSONRecord:
		w.maskRecord()
	}
}

func (w *liveDiagnosticCapture) maskRecord() {
	w.record = ""
	_, _ = io.WriteString(w.tee, w.stream.Flush())
	_, _ = io.WriteString(w.tee, w.redactor.Mask()+"\n")
}

func mayOpenJSONRecord(partial string) bool {
	trimmed := strings.TrimLeft(partial, " \t\r")
	return trimmed == "" || trimmed[0] == '{' || trimmed[0] == '['
}

func opensJSONRecord(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || (trimmed[0] != '{' && trimmed[0] != '[') || json.Valid([]byte(trimmed)) {
		return false
	}
	rest := strings.TrimLeft(trimmed[1:], " \t")
	return rest == "" || rest[0] == '"'
}
