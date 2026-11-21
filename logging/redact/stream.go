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
}

type streamRecord struct {
	data       []byte
	start, end int64
	delimiter  byte
}

type byteSpan struct {
	start, end int64
}

func NewStream(redactor *Redactor) *Stream {
	return &Stream{base: redactor}
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
			s.ingest(chunk, 0)
			break
		}
		s.ingest(chunk[:i], chunk[i])
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

	if s.offset > s.lineStart {
		s.finishLine(0)
	}
	var output strings.Builder
	s.drain(&output, s.offset)
	s.search = nil
	s.offset, s.lineStart = 0, 0
	s.secrets = nil
	return output.String()
}

func (s *Stream) ingest(text string, delimiter byte) {
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
	for len(s.secrets) != 0 {
		previous := s.secrets[len(s.secrets)-1]
		if previous.end <= span.start {
			break
		}
		span.start = min(span.start, previous.start)
		s.secrets = s.secrets[:len(s.secrets)-1]
	}
	s.secrets = append(s.secrets, span)
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
