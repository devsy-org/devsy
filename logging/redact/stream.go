package redact

import (
	"strings"
	"sync"
)

const streamLineLimit = 16 * 1024

type Stream struct {
	mu        sync.Mutex
	base      *Redactor
	line      []byte
	lineStart int64
	offset    int64
	search    *matchNode
	records   []streamRecord
	secrets   []byteSpan
	keepPlain bool
	plain     *plainRecord
}

type streamRecord struct {
	data       []byte
	start, end int64
	delimiter  byte
}

type byteSpan struct {
	start, end int64
	width      int64
}

func NewStream(redactor *Redactor) *Stream {
	return &Stream{base: redactor}
}

func NewDiagnosticStream(redactor *Redactor) *Stream {
	return &Stream{base: redactor, keepPlain: redactor != nil && redactor.canKeepPlain()}
}

func (r *Redactor) canKeepPlain() bool {
	mask := r.Mask()
	if strings.ContainsAny(mask, "\r\n") {
		return false
	}
	var forbidden [256]bool
	forbidden['\r'], forbidden['\n'] = true, true
	for index := range len(mask) {
		forbidden[mask[index]] = true
	}
	for _, value := range r.values {
		for index := range len(value) {
			if forbidden[value[index]] {
				return false
			}
		}
	}
	return true
}

func (s *Stream) RedactChunk(chunk string) string {
	if s == nil || s.base == nil {
		return chunk
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var output strings.Builder
	for chunk != "" {
		i := strings.IndexAny(chunk, "\r\n")
		if i < 0 {
			s.ingest(chunk, 0, &output)
			break
		}
		s.ingest(chunk[:i], chunk[i], &output)
		chunk = chunk[i+1:]
		s.drain(&output, s.safeOffset())
	}
	s.drain(&output, s.safeOffset())
	return output.String()
}

func (s *Stream) Flush() string {
	if s == nil || s.base == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var output strings.Builder
	if s.plain != nil {
		s.plain.emit(&output, s.plain.end, s.base.Mask())
		s.plain = nil
	} else if s.offset > s.lineStart {
		s.finishLine(0)
	}
	s.drain(&output, s.offset)
	s.search = nil
	s.offset, s.lineStart = 0, 0
	s.secrets = nil
	return output.String()
}

func (s *Stream) ingest(text string, delimiter byte, output *strings.Builder) {
	if s.keepPlain && (s.plain != nil || s.startPlain(text, output)) {
		s.writePlain(text, delimiter, output)
		return
	}
	if s.offset-s.lineStart <= streamLineLimit {
		if len(text) > streamLineLimit-len(s.line) {
			s.line = nil
		} else {
			s.line = append(s.line, text...)
		}
	}
	s.scan(text)
	if delimiter != 0 {
		s.scanDelimiter(delimiter)
		s.finishLine(delimiter)
	}
}

func (s *Stream) startPlain(text string, output *strings.Builder) bool {
	if s.offset-s.lineStart > streamLineLimit || len(text) <= streamLineLimit-len(s.line) {
		return false
	}
	const protected = ":="
	prefix := text[:streamLineLimit-len(s.line)]
	if strings.ContainsAny(string(s.line), protected) || strings.ContainsAny(prefix, protected) ||
		!isPlainStart(string(s.line), prefix) {
		return false
	}
	s.plain = &plainRecord{}
	s.plain.write(output, string(s.line), s.base)
	s.line = nil
	return true
}

func isPlainStart(buffer, prefix string) bool {
	start := strings.TrimLeft(buffer, " \t")
	if start == "" {
		start = strings.TrimLeft(prefix, " \t")
	}
	return start != "" && !strings.ContainsRune(`{["-0123456789tfn`, rune(start[0]))
}

func (s *Stream) writePlain(text string, delimiter byte, output *strings.Builder) {
	s.plain.write(output, text, s.base)
	s.offset += int64(len(text))
	if delimiter == 0 {
		return
	}
	s.plain.emit(output, s.plain.end, s.base.Mask())
	output.WriteByte(delimiter)
	s.plain = nil
	s.search = nil
	s.offset++
	s.lineStart = s.offset
}

func (s *Stream) scan(text string) {
	if s.base.matcher == nil {
		s.offset += int64(len(text))
		return
	}
	for i := range len(text) {
		s.scanByte(text[i], false)
	}
}

func (s *Stream) scanDelimiter(delimiter byte) {
	if s.base.matcher == nil {
		s.offset++
		return
	}
	s.scanByte(delimiter, true)
}

func (s *Stream) scanByte(value byte, delimiter bool) {
	root := s.base.matcher.forward
	if s.search == nil {
		s.search = root
	}
	s.search = transition(root, s.search, value)
	s.offset++
	start := s.offset - int64(s.search.longest)
	if s.search.longest > 0 && (delimiter || start < s.lineStart) {
		s.addSecret(byteSpan{start: start, end: s.offset})
	}
}

func (s *Stream) addSecret(span byteSpan) {
	s.secrets = appendSpan(s.secrets, span)
}

func appendSpan(spans []byteSpan, span byteSpan) []byteSpan {
	for len(spans) != 0 {
		previous := spans[len(spans)-1]
		if previous.end <= span.start {
			break
		}
		if previous.start < span.start {
			span.start = previous.start
			span.width = previous.width
		} else if previous.start == span.start {
			span.width = max(previous.width, span.width)
		}
		spans = spans[:len(spans)-1]
	}
	return append(spans, span)
}

func (s *Stream) finishLine(delimiter byte) {
	s.records = append(s.records, streamRecord{
		data: s.line, start: s.lineStart, end: s.offset, delimiter: delimiter,
	})
	s.line = nil
	s.lineStart = s.offset
}

func (s *Stream) safeOffset() int64 {
	if s.search == nil {
		return s.offset
	}
	return s.offset - int64(s.search.proper)
}

func (s *Stream) drain(output *strings.Builder, limit int64) {
	for len(s.records) > 0 && s.records[0].end <= limit {
		record := s.records[0]
		s.records[0] = streamRecord{}
		s.records = s.records[1:]
		s.writeRecord(output, record)
	}
	if len(s.records) == 0 {
		s.records = nil
	}
	for len(s.secrets) > 0 && s.secrets[0].end <= s.lineStart && len(s.records) == 0 {
		s.secrets = s.secrets[1:]
	}
}

func (s *Stream) writeRecord(output *strings.Builder, record streamRecord) {
	for len(s.secrets) > 0 && s.secrets[0].end <= record.start {
		s.secrets = s.secrets[1:]
	}
	output.WriteString(s.recordText(record))
	if record.delimiter != 0 {
		output.WriteByte(record.delimiter)
	}
}

func (s *Stream) recordText(record streamRecord) string {
	switch {
	case record.contentLength() == 0:
		return ""
	case record.contentLength() > streamLineLimit:
		return s.base.jsonMask()
	case len(s.secrets) > 0 && s.secrets[0].start < record.end:
		return s.base.jsonMask()
	default:
		return s.base.Redact(string(record.data))
	}
}

func (r streamRecord) contentLength() int64 {
	length := r.end - r.start
	if r.delimiter != 0 {
		length--
	}
	return length
}
