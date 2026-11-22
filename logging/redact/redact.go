package redact

import (
	"regexp"
	"strings"
)

const (
	defaultMask         = "***"
	authorizationField  = "Authorization"
	headerStart         = `(?i)\b((?:proxy-)?authorization[ \t]*[:=][ \t]*)`
	lineBreak           = `(?:\r\n|\r|\n)`
	authorizationScheme = `(?:basic|bearer)\b`
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
	values        []string
	matcher       *byteMatcher
	escaped       *byteMatcher
	maxLength     int
	mask          string
	authorityURLs bool
	foldedHeaders bool
}

var (
	urlPattern          = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^\r\n]*@`)
	authorityURLPattern = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/\s\x0b\x85\pZ?#"<>]*@`)
	headerPattern       = regexp.MustCompile(headerStart + `[^\r\n]*`)
	foldedHeaderPattern = regexp.MustCompile(
		headerStart + `(?:` + lineBreak + `[ \t]*` + authorizationScheme + `)?[^\r\n]*` +
			`(?:` + lineBreak + `[ \t]+[^ \t\r\n][^\r\n]*)*`,
	)
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
	if result, ok := r.redactJSON(value); ok {
		return result
	}
	return r.redactText(value)
}

func (r *Redactor) RedactDiagnostic(value string) string {
	if len(value) > streamLineLimit && !strings.ContainsAny(value, "\r\n:=") {
		stream := NewDiagnosticStream(r)
		if stream.keepPlain && isPlainStart("", value[:streamLineLimit]) {
			return stream.RedactChunk(value) + stream.Flush()
		}
	}
	var diagnostic Redactor
	if r != nil {
		diagnostic = *r
	}
	diagnostic.authorityURLs, diagnostic.foldedHeaders = true, true
	return diagnostic.Redact(value)
}

func (r *Redactor) MaskCredentialField(key string) (string, bool) {
	for _, name := range []string{
		authorizationField, "Proxy-Authorization", "Cookie", "Set-Cookie", "Cookie2", "Set-Cookie2",
	} {
		if strings.EqualFold(key, name) {
			return r.Mask(), true
		}
	}
	return "", false
}

func (r *Redactor) RedactField(key, value string) string {
	if mask, sensitive := r.MaskCredentialField(key); sensitive {
		return mask
	}
	return r.sanitizeString(value)
}

func (r *Redactor) ContainsValue(value string) bool {
	if r == nil || r.matcher == nil {
		return false
	}
	root := r.matcher.forward
	state := root
	for i := range len(value) {
		state = transition(root, state, value[i])
		if state.longest > 0 {
			return true
		}
	}
	return false
}

func (r *Redactor) RedactRegistered(value string) string {
	if r == nil || r.matcher == nil {
		return value
	}
	lengths := r.registeredLengths(value)
	var result strings.Builder
	covered := 0
	for i, length := range lengths {
		if length > 0 {
			if i >= covered {
				result.WriteString(value[covered:i])
				result.WriteString(r.Mask())
			}
			covered = max(covered, i+length)
		}
	}
	if result.Len() == 0 {
		return value
	}
	result.WriteString(value[covered:])
	return result.String()
}

func newRedactor(values []string, mask string) *Redactor {
	r := &Redactor{values: values, mask: mask}
	if len(values) == 0 {
		return r
	}
	r.matcher = newByteMatcher(values)
	r.escaped = newEscapedMatcher(values)
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

func (r *Redactor) Mask() string {
	if r == nil || r.mask == "" {
		return defaultMask
	}
	return r.mask
}

func (r *Redactor) registeredLengths(value string) []int {
	lengths := make([]int, len(value))
	for _, matcher := range []*byteMatcher{r.matcher, r.escaped} {
		for i, length := range matchLengthsWith(matcher, value, len(value)) {
			lengths[i] = max(lengths[i], length)
		}
	}
	return lengths
}

type textMatches struct {
	matches [][]int
	trim    int
}

func (m *textMatches) lengthAt(index int) int {
	if len(m.matches) == 0 || m.matches[0][3] != index {
		return 0
	}
	end := m.matches[0][1] - m.trim
	m.matches = m.matches[1:]
	return end - index
}

func (r *Redactor) textPatterns(value string) (urls, headers textMatches) {
	url, header := urlPattern, headerPattern
	if r != nil && r.authorityURLs {
		url = authorityURLPattern
	}
	if r != nil && r.foldedHeaders {
		header = foldedHeaderPattern
	}
	return textMatches{matches: url.FindAllStringSubmatchIndex(value, -1), trim: 1},
		textMatches{matches: header.FindAllStringSubmatchIndex(value, -1)}
}

func (r *Redactor) redactText(value string) string {
	lengths := r.matchLengths(value, len(value))
	urls, headers := r.textPatterns(value)
	var result strings.Builder
	covered := 0
	for i := range len(value) {
		length := max(urls.lengthAt(i), headers.lengthAt(i))
		if i < len(lengths) {
			length = max(length, lengths[i])
		}
		if length > 0 {
			if i >= covered {
				result.WriteString(value[covered:i])
				result.WriteString(r.Mask())
			}
			covered = max(covered, i+length)
		}
	}
	if result.Len() != 0 {
		result.WriteString(value[covered:])
		value = result.String()
	}
	if r.matchesString(value) {
		return r.Mask()
	}
	return value
}
