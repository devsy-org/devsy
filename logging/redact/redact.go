package redact

import (
	"encoding/json"
	"regexp"
	"strings"
)

const (
	defaultMask        = "***"
	authorizationField = "Authorization"
)

type Options struct {
	Values                    []string
	Environment               []string
	AdditionalEnvironmentKeys []string
	Mask                      string
	MinExplicitBytes          int
	SplitExplicitLines        bool
}

type Redactor struct {
	values    []string
	matcher   *byteMatcher
	maxLength int
	mask      string
}

const urlSchemePattern = `((?i:https?://)|[A-Za-z][A-Za-z0-9+.-]*://)`

const urlAuthorityByte = `(?:[^\s/?#"\x5c]|\x5cu[0-9a-fA-F]{4})`

var urlAuthority = regexp.MustCompile(
	urlSchemePattern + `(?:` + urlAuthorityByte + `+@|` + urlAuthorityByte + `+["\x5c])`,
)

func New(opts Options) *Redactor {
	mask := opts.Mask
	if mask == "" {
		mask = defaultMask
	}
	collector := valueCollector{
		values: make([]string, 0, len(opts.Values)+len(opts.Environment)),
		seen:   make(map[string]struct{}),
	}
	collector.explicit(opts.Values, max(opts.MinExplicitBytes, 1), opts.SplitExplicitLines)
	collector.environment(opts.Environment, opts.AdditionalEnvironmentKeys)
	return newRedactor(collector.values, mask)
}

type valueCollector struct {
	values []string
	seen   map[string]struct{}
}

func (c *valueCollector) add(value string, minimum int) {
	if len(value) < minimum {
		return
	}
	if _, ok := c.seen[value]; ok {
		return
	}
	c.seen[value] = struct{}{}
	c.values = append(c.values, strings.Clone(value))
}

func (c *valueCollector) explicit(values []string, minimum int, splitLines bool) {
	for _, value := range values {
		c.add(value, minimum)
		if splitLines {
			for _, line := range strings.Split(value, "\n") {
				c.add(line, minimum)
			}
		}
	}
}

func (c *valueCollector) environment(entries, additionalKeys []string) {
	for _, entry := range entries {
		key, value, ok := strings.Cut(entry, "=")
		if ok && sensitiveKey(key, additionalKeys) {
			c.add(value, 1)
		}
	}
}

func FromEntries(entries []string) *Redactor {
	values := make([]string, 0, len(entries))
	for _, entry := range entries {
		_, value, ok := strings.Cut(entry, "=")
		if ok && value != "" {
			values = append(values, value)
		}
	}
	return New(Options{Values: values})
}

func Combine(redactors ...*Redactor) *Redactor {
	var values []string
	for _, redactor := range redactors {
		if redactor != nil {
			values = append(values, redactor.values...)
		}
	}
	return New(Options{Values: values})
}

func (r *Redactor) Redact(value string) string {
	urls, headers := formatMatches(value)
	value, _, _ = r.known(value, matchState{cut: len(value), urls: urls, headers: headers})
	return value
}

func (r *Redactor) MaskCredentialField(key string) (string, bool) {
	if isCredentialField(key) {
		return r.maskText(), true
	}
	return "", false
}

func (r *Redactor) RedactField(key, value string) string {
	if !isCredentialField(key) || strings.TrimSpace(value) == "" {
		return r.Redact(value)
	}
	start, ok := explicitCredentialStart(key, value)
	if !ok {
		return r.maskText()
	}
	urls, headers := formatMatches(value)
	if start < len(value) {
		headers = insertCredentialSpan(headers, credentialSpan{start: start, end: len(value)})
	}
	value, _, _ = r.known(value, matchState{cut: len(value), urls: urls, headers: headers})
	return value
}

func explicitCredentialStart(key, value string) (int, bool) {
	if !strings.EqualFold(key, authorizationField) &&
		!strings.EqualFold(key, "Proxy-Authorization") {
		return 0, false
	}
	const whitespace = " \t\n\r\f"
	trimmed := strings.TrimLeft(value, whitespace)
	end := strings.IndexAny(trimmed, whitespace)
	if end < 0 {
		end = len(trimmed)
	}
	scheme := trimmed[:end]
	if !strings.EqualFold(scheme, "Basic") && !strings.EqualFold(scheme, "Bearer") {
		return 0, false
	}
	return len(value) - len(strings.TrimLeft(trimmed[end:], whitespace)), true
}

func insertCredentialSpan(spans []credentialSpan, mask credentialSpan) []credentialSpan {
	for i, span := range spans {
		if mask.start < span.start {
			return append(append(spans[:i:i], mask), spans[i:]...)
		}
	}
	return append(spans, mask)
}

func newRedactor(values []string, mask string) *Redactor {
	r := &Redactor{values: values, mask: mask}
	if len(values) == 0 {
		return r
	}
	r.matcher = newByteMatcher(values)
	for _, value := range values {
		r.maxLength = max(r.maxLength, len(value))
	}
	return r
}

func sensitiveKey(key string, additional []string) bool {
	key = strings.ToUpper(key)
	for _, marker := range []string{
		"PASSWORD", "PASSWD", "TOKEN", "SECRET", "API_KEY", "APIKEY",
		"AUTH", "CREDENTIAL", "PRIVATE_KEY", "ACCESS_KEY",
	} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	for _, extra := range additional {
		if strings.EqualFold(key, extra) {
			return true
		}
	}
	return false
}

func (r *Redactor) maskText() string {
	if r == nil || r.mask == "" {
		return defaultMask
	}
	return r.mask
}

type matchState struct {
	cut       int
	covered   int
	markerEnd int
	urls      [][]int
	headers   []credentialSpan
}

type formatCursor struct {
	matches [][]int
	index   int
	trim    int
}

type maskWriter struct {
	result    strings.Builder
	redactor  *Redactor
	cut       int
	covered   int
	markerEnd int
	last      int
	changed   bool
}

func (r *Redactor) known(value string, state matchState) (string, int, int) {
	output := maskWriter{
		redactor: r, cut: state.cut, covered: state.covered,
		markerEnd: state.markerEnd, changed: state.covered > 0,
	}
	if output.changed {
		output.result.Grow(state.cut)
	}
	urls := formatCursor{matches: state.urls, trim: 1}
	headers := state.headers
	lengths := r.matchLengths(value, state.cut)
	for i := range state.cut {
		length := 0
		if i < len(lengths) {
			length = lengths[i]
		}
		length = max(length, urls.matchLength(i))
		var style credentialMaskStyle
		for len(headers) > 0 && headers[0].start == i {
			length = max(length, headers[0].end-i)
			style = max(style, headers[0].style)
			headers = headers[1:]
		}
		output.consume(value, i, length, style)
	}
	if !output.changed {
		return value[:state.cut], 0, 0
	}
	output.result.WriteString(value[output.last:state.cut])
	return output.result.String(), max(
			0,
			output.covered-state.cut,
		), max(
			0,
			output.markerEnd-state.cut,
		)
}

func (r *Redactor) credentialMask(style credentialMaskStyle) string {
	mask := r.maskText()
	switch style {
	case credentialBareJSON:
		return "null"
	case credentialInJSON:
		encoded, _ := json.Marshal(mask)
		return string(encoded[1 : len(encoded)-1])
	default:
		return mask
	}
}

func (w *maskWriter) consume(value string, index, length int, style credentialMaskStyle) {
	if length != 0 {
		if index >= w.markerEnd {
			if !w.changed {
				w.result.Grow(w.cut)
				w.changed = true
			}
			w.result.WriteString(value[w.last:index])
			w.result.WriteString(w.redactor.credentialMask(style))
			w.markerEnd = index + length
		}
		w.covered = max(w.covered, index+length)
	}
	if index < w.covered {
		w.last = index + 1
	}
}

func (c *formatCursor) matchLength(index int) int {
	length := 0
	for c.index < len(c.matches) && c.matches[c.index][3] == index {
		end := c.matches[c.index][1] - c.trim
		length = max(length, end-index)
		c.index++
	}
	return length
}
