package redact

import (
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

var (
	credentialURL = regexp.MustCompile(`(?i)(https?://)[^\s/@?#]+@`)
	authorization = regexp.MustCompile(`(?i)(authorization\s*[:=]\s*(?:bearer|basic)\s+)[^\s,]+`)
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
	urls, headers := formatMatches(value, 0)
	value, _, _ = r.known(value, matchState{cut: len(value), urls: urls, headers: headers})
	return value
}

func (r *Redactor) RedactField(key, value string) string {
	if !strings.EqualFold(key, authorizationField) {
		return r.Redact(value)
	}
	urls, headers := formatMatches(value, 0)
	fieldHeaders := scanAuthorization(
		value,
		len(value),
		authorizationState{phase: authBeforeScheme},
	)
	headers = mergeHeaderMatches(headers, fieldHeaders)
	value, _, _ = r.known(value, matchState{cut: len(value), urls: urls, headers: headers})
	return value
}

func mergeHeaderMatches(first, second [][]int) [][]int {
	if len(first) == 0 {
		return second
	}
	if len(second) == 0 {
		return first
	}
	result := make([][]int, 0, len(first)+len(second))
	i, j := 0, 0
	for i < len(first) && j < len(second) {
		if first[i][3] <= second[j][3] {
			result = append(result, first[i])
			i++
		} else {
			result = append(result, second[j])
			j++
		}
	}
	result = append(result, first[i:]...)
	return append(result, second[j:]...)
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
	headers   [][]int
}

type formatCursor struct {
	matches [][]int
	index   int
	trim    int
}

type maskWriter struct {
	result    strings.Builder
	mask      string
	cut       int
	covered   int
	markerEnd int
	last      int
	changed   bool
}

func (r *Redactor) known(value string, state matchState) (string, int, int) {
	output := maskWriter{
		mask: r.maskText(), cut: state.cut, covered: state.covered,
		markerEnd: state.markerEnd, changed: state.covered > 0,
	}
	if output.changed {
		output.result.Grow(state.cut)
	}
	urls := formatCursor{matches: state.urls, trim: 1}
	headers := formatCursor{matches: state.headers}
	lengths := r.matchLengths(value, state.cut)
	for i := range state.cut {
		length := 0
		if i < len(lengths) {
			length = lengths[i]
		}
		length = max(length, urls.matchLength(i), headers.matchLength(i))
		output.consume(value, i, length)
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

func (w *maskWriter) consume(value string, index, length int) {
	if length != 0 {
		if index >= w.markerEnd {
			if !w.changed {
				w.result.Grow(w.cut)
				w.changed = true
			}
			w.result.WriteString(value[w.last:index])
			w.result.WriteString(w.mask)
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
