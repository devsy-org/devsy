package redact

import "strings"

type matchSpan struct{ start, end int }

func (r *Redactor) RedactRegistered(value string) string {
	if r == nil || r.matcher == nil {
		return value
	}
	scan := registeredScan{value: value, mask: r.Mask(), window: r.maxLength}
	rawRoot := r.matcher.forward
	rawState := rawRoot
	escapedRoot, escapedWindow := r.escaped.forward()
	escapedState := escapedRoot
	scan.window = max(scan.window, escapedWindow)
	for i := range len(value) {
		rawState = transition(rawRoot, rawState, value[i])
		length := rawState.longest
		if escapedRoot != nil {
			escapedState = transition(escapedRoot, escapedState, value[i])
			length = max(length, escapedState.longest)
		}
		scan.advance(i+1, length)
	}
	return scan.finish()
}

type registeredScan struct {
	value   string
	mask    string
	window  int
	result  strings.Builder
	pending []matchSpan
	head    int
	cursor  int
	masked  bool
}

func (s *registeredScan) advance(position, length int) {
	if length > 0 {
		span := matchSpan{start: position - length, end: position}
		for len(s.pending) > s.head && span.start <= s.pending[len(s.pending)-1].end {
			span.start = min(span.start, s.pending[len(s.pending)-1].start)
			s.pending = s.pending[:len(s.pending)-1]
		}
		s.pending = append(s.pending, span)
	}
	for s.head < len(s.pending) && s.pending[s.head].end < position+1-s.window {
		s.emit(s.pending[s.head])
		s.head++
	}
	s.compact()
}

func (s *registeredScan) compact() {
	if s.head == len(s.pending) {
		s.pending, s.head = s.pending[:0], 0
	} else if s.head >= 4096 && s.head >= len(s.pending)-s.head {
		s.pending = append(s.pending[:0], s.pending[s.head:]...)
		s.head = 0
	}
}

func (s *registeredScan) emit(span matchSpan) {
	s.masked = true
	s.result.Grow(span.start - s.cursor + len(s.mask))
	s.result.WriteString(s.value[s.cursor:span.start])
	s.result.WriteString(s.mask)
	s.cursor = span.end
}

func (s *registeredScan) finish() string {
	for _, span := range s.pending[s.head:] {
		s.emit(span)
	}
	if !s.masked {
		return s.value
	}
	s.result.Grow(len(s.value) - s.cursor)
	s.result.WriteString(s.value[s.cursor:])
	return s.result.String()
}
