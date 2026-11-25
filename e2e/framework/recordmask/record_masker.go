package recordmask

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"

	"github.com/devsy-org/devsy/internal/diagnosticredact"
	"github.com/devsy-org/devsy/logging/redact"
)

const maxJSONRecordBytes = 64 << 10

// streamLineLimit and headerStart mirror the diagnostic stream; tests pin them.
const streamLineLimit = 16 << 10

var headerStart = regexp.MustCompile(`(?i)\b(?:proxy-)?authorization[ \t]*[:=]`)

// Masker masks JSON records and plain text from streamed output.
type Masker struct {
	redactor *redact.Redactor
	stream   *redact.Stream
	emit     func(string)
	pending  []byte
	lead     string
	noEOL    int
	midLine  bool
	maskNext bool
	unit     lineUnit
	skip     *recordSkipper
}

// lineUnit is one logical line: a physical line plus the fold lines after it
// (lines that start with a space or tab). A unit that contains '{' or '['
// followed, after optional whitespace, by a quote or backslash is masked whole.
// A unit also continues past a line break that follows such a bracket, so the
// next line is judged with it. It holds at most maxJSONRecordBytes.
type lineUnit struct {
	buf       []byte
	lineStart int
	active    bool
	open      bool
	hit       bool
	discard   bool
	quiet     bool
	lead      bool
	last      byte
	tail      byte
	run       int
	wide      bool
	rec       bool
	hdr       bool
}

func isBlank(c byte) bool  { return c == ' ' || c == '\t' }
func isEOL(c byte) bool    { return c == '\r' || c == '\n' }
func isSpace(c byte) bool  { return isBlank(c) || isEOL(c) }
func isOpener(c byte) bool { return c == '{' || c == '[' }

// boundary returns the buf index where the unit ends when c arrives, or -1.
// A fold line that starts with a bracket is left to the record path, so the
// unit ends before its indentation, unless the unit is already masked.
func (u *lineUnit) boundary(c byte) int {
	if u.lead && !isBlank(c) {
		u.lead = false
		return u.handoff(c)
	}
	if !u.atLineStart(c) || u.open {
		return -1
	}
	if !isBlank(c) {
		return len(u.buf)
	}
	u.hit = u.hit || u.rec && u.folds()
	u.lead, u.lineStart = true, len(u.buf)
	return -1
}

// handoff returns where a bracket-start fold line begins, or -1 to keep the
// line in the unit. lineStart is stale once the unit is discarding.
func (u *lineUnit) handoff(c byte) int {
	if isOpener(c) && !u.open && !u.hit && !u.discard {
		return u.lineStart
	}
	return -1
}

func (u *lineUnit) atLineStart(c byte) bool {
	return isEOL(u.last) && (u.last != '\r' || c != '\n')
}

func (u *lineUnit) scan(c byte) {
	if isSpace(c) {
		return
	}
	u.hit = u.hit || u.open && (c == '"' || c == '\\')
	u.open = isOpener(c)
}

// folds reports whether the stream, which saw the original line of a rewritten
// record, would fold the next line.
func (u *lineUnit) folds() bool {
	return u.wide || u.hdr && u.tail != 0
}

func (u *lineUnit) count(c byte) {
	switch {
	case !isEOL(c):
		u.run++
	case u.run > 0:
		u.wide, u.run = u.run > streamLineLimit, 0
	}
}

func (u *lineUnit) add(c byte) {
	u.scan(c)
	u.count(c)
	u.last = c
	if !isSpace(c) {
		u.tail = c
	}
	u.lead = u.lead && !isEOL(c)
	if !u.discard {
		u.buf = append(u.buf, c)
	}
}

// recordSkipper tracks JSON nesting so an oversized record can be discarded
// without buffering until its closing bracket arrives.
type recordSkipper struct {
	depth    int
	inString bool
	escaped  bool
	run      int
}

// consume returns the text after the record's end and whether the record closed.
func (s *recordSkipper) consume(text []byte) ([]byte, bool) {
	for i, c := range text {
		s.run++
		if isEOL(c) {
			s.run = 0
		}
		if s.advance(c) {
			return text[i+1:], true
		}
	}
	return nil, false
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

// New returns a Masker that sends masked text to emit.
func New(redactor *redact.Redactor, emit func(string)) *Masker {
	return &Masker{
		redactor: redactor,
		stream:   diagnosticredact.Stream(redactor),
		emit:     emit,
	}
}

// Write feeds output to the masker; masked text reaches emit as records complete.
func (m *Masker) Write(text string) {
	m.pending = append(m.pending, text...)
	m.drain(false)
}

// Finish flushes the pending record. Call it once after the last Write.
func (m *Masker) Finish() {
	m.drain(true)
	if m.unit.active {
		m.endUnit(true, 0)
	}
	m.emit(m.stream.Flush())
}

func (m *Masker) drain(final bool) {
	for len(m.pending) > 0 && m.step(final) {
	}
}

func (m *Masker) step(final bool) bool {
	if m.skip != nil {
		return m.stepSkip()
	}
	if m.unit.active {
		return m.stepUnit()
	}
	return m.stepLine(final)
}

func (m *Masker) stepUnit() bool {
	m.pending = m.feedUnit(m.pending)
	return !m.unit.active
}

// stepLine decides from at most the first maxJSONRecordBytes bytes of the
// physical line, held indentation included, so the outcome does not depend on
// how the input was split into writes.
func (m *Masker) stepLine(final bool) bool {
	budget := maxJSONRecordBytes - len(m.lead)
	over := len(m.pending) > budget
	view := m.pending[:min(len(m.pending), budget)]
	blank := len(view) - len(bytes.TrimLeft(view, " \t"))
	start := view[blank:]
	if m.maskNext || m.midLine || len(start) > 0 && !isOpener(start[0]) {
		return m.beginUnit()
	}
	if len(start) == 0 {
		return (final || over) && m.beginUnit()
	}
	return m.maskRecord(start, blank, over, final)
}

func (m *Masker) stepSkip() bool {
	rest, closed := m.skip.consume(m.pending)
	m.pending = rest
	if closed {
		m.beginQuiet(m.skip.run)
		m.skip = nil
	}
	return closed
}

// beginUnit starts a unit at the unread text. A record is only decoded at the
// start of a physical line, so text after a record on its line comes here.
func (m *Masker) beginUnit() bool {
	m.unit.active, m.midLine, m.noEOL = true, false, 0
	m.unit.run += len(m.lead)
	m.unit.hit = m.unit.hit || m.maskNext
	m.maskNext = false
	m.unit.buf = append(m.unit.buf, m.lead...)
	m.lead = ""
	return true
}

// beginQuiet drops the rest of the line and its fold lines after a skipped
// record, whose mask already ended with a line break.
func (m *Masker) beginQuiet(run int) {
	m.unit.discard, m.unit.quiet, m.unit.run = true, true, run
	m.beginUnit()
}

// feedUnit consumes text into the current unit and returns what follows it.
// Indentation held back for a bracket-start fold line moves to m.lead.
func (m *Masker) feedUnit(text []byte) []byte {
	u := &m.unit
	for i, c := range text {
		if cut := u.boundary(c); cut >= 0 {
			m.lead = string(u.buf[cut:])
			u.buf = u.buf[:cut]
			m.endUnit(false, c)
			return text[i:]
		}
		if !u.discard && len(u.buf) >= maxJSONRecordBytes {
			u.buf, u.discard = u.buf[:0], true
			m.plain(m.redactor.Mask())
		}
		u.add(c)
	}
	return text[:0]
}

func (m *Masker) endUnit(final bool, next byte) {
	u := &m.unit
	line := string(u.buf)
	switch {
	case u.discard:
		line = ""
		if !final && !u.quiet {
			line = "\n"
		}
	case u.hit:
		body := strings.TrimRight(line, "\r\n")
		line = m.redactor.Mask() + line[len(body):]
	}
	m.markFold(u, next)
	m.plain(line)
	*u = lineUnit{buf: u.buf[:0]}
}

// markFold masks the next line as the stream folds it: after a header
// separator, or after a long line when it starts with a scheme word ('b' here).
func (m *Masker) markFold(u *lineUnit, next byte) {
	masked := u.discard || u.hit
	wide := (masked || u.rec) && u.folds() && next|0x20 == 'b'
	m.maskNext = masked && (u.tail == ':' || u.tail == '=') || wide
}

func (m *Masker) plain(text string) {
	m.emit(m.stream.RedactChunk(text))
}

func (m *Masker) maskRecord(start []byte, blank int, over, final bool) bool {
	if !final && !over && !m.hasEOL() {
		return false
	}
	var record json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(start))
	err := decoder.Decode(&record)
	switch {
	case err == nil:
		m.rewrite(blank, blank+int(decoder.InputOffset()))
		return true
	case !errors.Is(err, io.ErrUnexpectedEOF):
		return m.beginUnit()
	case over:
		return m.skipOversized()
	case final:
		m.maskPending()
		return true
	}
	return false
}

// rewrite emits the field-masked record. The stream only sees the masked text,
// so the unit that follows takes over the state of the original line.
func (m *Masker) rewrite(blank, end int) {
	lead := m.lead + string(m.pending[:blank])
	text := string(m.pending[blank:end])
	m.plain(lead + diagnosticredact.Record(m.redactor, text))
	line := lead + text
	m.unit.rec, m.unit.hdr = true, headerStart.MatchString(line)
	m.unit.run = len(line) - strings.LastIndexAny(line, "\r\n") - 1
	m.pending, m.lead, m.midLine, m.noEOL = m.pending[end:], "", true, 0
}

// hasEOL searches only bytes not yet known to lack a line break, so a record
// that arrives byte by byte is scanned once.
func (m *Masker) hasEOL() bool {
	i := bytes.IndexAny(m.pending[m.noEOL:], "\r\n")
	if i < 0 {
		m.noEOL = len(m.pending)
	}
	return i >= 0
}

// skipOversized masks a record that is still open after the limit. Whether
// that is so depends only on the first maxJSONRecordBytes bytes.
func (m *Masker) skipOversized() bool {
	skip := &recordSkipper{run: len(m.lead)}
	rest, closed := skip.consume(m.pending)
	m.maskPending()
	if closed {
		m.pending = rest
		m.beginQuiet(skip.run)
	} else {
		m.skip = skip
	}
	return true
}

func (m *Masker) maskPending() {
	m.pending, m.lead, m.noEOL = nil, "", 0
	m.emit(m.stream.Flush())
	m.emit(m.redactor.Mask() + "\n")
}

// Text masks value in one pass.
func Text(redactor *redact.Redactor, value string) string {
	var out strings.Builder
	masker := New(redactor, func(text string) { out.WriteString(text) })
	masker.Write(value)
	masker.Finish()
	return out.String()
}

// ReportText masks value for failure reports. JSON records are masked first
// because the whole-buffer scan misses records mixed with log lines.
func ReportText(redactor *redact.Redactor, value string) string {
	return diagnosticredact.Record(redactor, Text(redactor, value))
}
