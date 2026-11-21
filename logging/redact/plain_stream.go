package redact

import (
	"bytes"
	"strings"
)

type plainRecord struct {
	pending    bytes.Buffer
	search     *matchNode
	spans      []byteSpan
	start, end int64
	nextMask   int64
	suppressed bool
}

func (p *plainRecord) write(output *strings.Builder, text string, redactor *Redactor) {
	if p.suppressed {
		return
	}
	boundary := strings.IndexAny(text, ":=")
	if boundary >= 0 {
		text = text[:boundary]
	}
	mask := redactor.Mask()
	for len(text) > 0 {
		size := min(len(text), streamLineLimit)
		limit := p.scan(text[:size], redactor.matcher)
		p.emit(output, limit, mask)
		text = text[size:]
	}
	if boundary >= 0 {
		if len(p.spans) == 0 || p.spans[0].start >= p.start {
			output.WriteString(mask)
		}
		p.pending = bytes.Buffer{}
		p.spans = nil
		p.start = p.end
		p.suppressed = true
	}
}

func (p *plainRecord) scan(text string, matcher *byteMatcher) int64 {
	p.pending.WriteString(text)
	if matcher == nil {
		p.end += int64(len(text))
		return p.end
	}
	root := matcher.forward
	if p.search == nil {
		p.search = root
	}
	for index := range len(text) {
		p.search = transition(root, p.search, text[index])
		p.end++
		if p.search.longest > 0 {
			width := int64(p.search.longest)
			p.spans = appendSpan(p.spans, byteSpan{start: p.end - width, end: p.end, width: width})
		}
	}
	return p.end - int64(p.search.proper)
}

func (p *plainRecord) emit(output *strings.Builder, limit int64, mask string) {
	consumed := 0
	for _, span := range p.spans {
		if span.start >= limit {
			break
		}
		if span.start >= p.start {
			output.Write(p.take(span.start))
			p.nextMask = span.start
		}
		end := min(span.end, limit)
		for p.nextMask < end {
			output.WriteString(mask)
			p.nextMask += span.width
		}
		p.take(end)
		if span.end > limit {
			break
		}
		consumed++
	}
	if consumed == len(p.spans) {
		p.spans = p.spans[:0]
	} else {
		p.spans = p.spans[consumed:]
	}
	output.Write(p.take(limit))
}

func (p *plainRecord) take(end int64) []byte {
	data := p.pending.Next(int(end - p.start))
	p.start = end
	return data
}
