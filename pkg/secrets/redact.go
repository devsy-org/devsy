package secrets

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

const redactMask = "***"

var (
	credentialURLPattern = regexp.MustCompile(`(?i)(https?://)[^\s/@]+@`)
	authorizationPattern = regexp.MustCompile(
		`(?i)(authorization\s*[:=]\s*(?:bearer|basic)\s+)[^\s,]+`,
	)
)

type Redactor struct {
	replacer  *strings.Replacer
	values    []string
	maxLength int
	matcher   *secretNode
}

// NewRedactor masks the values (not keys) of KEY=VALUE entries; empty values are ignored.
func NewRedactor(secretsEnv []string) *Redactor {
	values := make([]string, 0, len(secretsEnv))
	for _, entry := range secretsEnv {
		_, value, ok := strings.Cut(entry, "=")
		if !ok || value == "" {
			continue
		}
		values = append(values, value)
	}
	if len(values) == 0 {
		return &Redactor{}
	}

	// Mask longer values first so an overlapping prefix (e.g. "sec" of "secret")
	// cannot partially match ahead of the full value.
	sort.SliceStable(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	pairs := make([]string, 0, len(values)*2)
	for _, v := range values {
		pairs = append(pairs, v, redactMask)
	}

	return &Redactor{
		replacer:  strings.NewReplacer(pairs...),
		values:    values,
		maxLength: len(values[0]),
		matcher:   newSecretMatcher(values),
	}
}

// Combine returns a redactor that masks the values known by every input.
// Nil redactors are ignored.
func Combine(redactors ...*Redactor) *Redactor {
	entries := make([]string, 0)
	for _, redactor := range redactors {
		if redactor == nil {
			continue
		}
		for i, value := range redactor.values {
			entries = append(entries, fmt.Sprintf("DEVSY_COMBINED_%d=%s", i, value))
		}
	}
	return NewRedactor(entries)
}

// logTextEscaper mirrors the escapes oneLineTextEncoder applies in pkg/log.
// It is duplicated here because pkg/log imports this package.
var logTextEscaper = newLogTextEscaper()

func newLogTextEscaper() *strings.Replacer {
	pairs := []string{
		"\n", `\n`,
		"\r", `\r`,
		"\v", `\v`,
		"\f", `\f`,
		"\x00", `\x00`,
		"\x7f", `\x7f`,
		"\u0085", `\u0085`,
		"\u2028", `\u2028`,
		"\u2029", `\u2029`,
	}
	for b := byte(1); b < 0x20; b++ {
		switch b {
		case '\t', 0x1b, '\n', '\r', '\v', '\f':
			continue
		}
		pairs = append(pairs, string([]byte{b}), fmt.Sprintf(`\x%02x`, b))
	}
	return strings.NewReplacer(pairs...)
}

// WithLogEscapes returns a redactor that also masks every value of r in the
// escaped forms the log encoders serialize it in (strconv, JSON, zap JSON and
// text-record escapes). Redaction runs on serialized records, so raw values
// alone would miss secrets containing quotes or control characters. A nil r
// yields a redactor with only format-based masking.
func WithLogEscapes(r *Redactor) *Redactor {
	if r == nil {
		return NewRedactor(nil)
	}
	entries := make([]string, 0, len(r.values)*5)
	for i, original := range r.values {
		for j, variant := range logEscapeVariants(original) {
			entries = append(entries, fmt.Sprintf("DEVSY_LOG_VALUE_%d_%d=%s", i, j, variant))
		}
	}
	return NewRedactor(entries)
}

func logEscapeVariants(value string) []string {
	quoted := strconv.Quote(value)
	encoded, _ := json.Marshal(value) // cannot fail for a string
	zapJSON := zapJSONEscape(value)
	return uniqueNonemptyStrings([]string{
		value,
		quoted[1 : len(quoted)-1],
		logTextEscaper.Replace(value),
		string(encoded[1 : len(encoded)-1]),
		zapJSON,
		// Text fields are zap JSON, then escaped again as a text record.
		logTextEscaper.Replace(zapJSON),
	})
}

// zapJSONEscape reproduces zap's JSON string escaping, which differs from
// encoding/json for \b, \f and U+2028/U+2029.
func zapJSONEscape(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	for i := 0; i < len(value); {
		r, width := utf8.DecodeRuneInString(value[i:])
		switch {
		case r == utf8.RuneError && width == 1:
			b.WriteString(`\ufffd`)
		case r == '\\' || r == '"':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteString(value[i : i+width])
		}
		i += width
	}
	return b.String()
}

func uniqueNonemptyStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

// NewEnvironmentRedactor protects values from environment variables that are
// conventionally credential-bearing, while leaving ordinary environment
// values available in diagnostics.
func NewEnvironmentRedactor(env []string) *Redactor {
	var sensitive []string
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if ok && isSensitiveEnvironmentKey(key) {
			sensitive = append(sensitive, entry)
		}
	}
	return NewRedactor(sensitive)
}

func isSensitiveEnvironmentKey(key string) bool {
	key = strings.ToUpper(key)
	for _, marker := range []string{
		"PASSWORD", "PASSWD", "TOKEN", "SECRET", "API_KEY", "APIKEY",
		"AUTH", "CREDENTIAL", "PRIVATE_KEY", "ACCESS_KEY",
	} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}

func (r *Redactor) Redact(s string) string {
	if r != nil && r.replacer != nil {
		s = r.replacer.Replace(s)
	}
	// Format-based masking protects credentials supplied directly in a URL or
	// header and therefore not available as environment values.
	s = credentialURLPattern.ReplaceAllString(s, "$1***@")
	return authorizationPattern.ReplaceAllString(s, "$1***")
}

// StreamingRedactor masks secrets split across text writes, buffering a short
// suffix until the next write or Flush.
type StreamingRedactor struct {
	mu      sync.Mutex
	base    *Redactor
	pending string
}

// NewStreamingRedactor creates a chunk-safe redactor around r.
func NewStreamingRedactor(r *Redactor) *StreamingRedactor {
	return &StreamingRedactor{base: r}
}

// RedactChunk returns the portion safe to emit immediately.
func (r *StreamingRedactor) RedactChunk(chunk string) string {
	if r == nil {
		return chunk
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	combined := r.pending + chunk
	r.pending = ""
	if r.base == nil {
		return combined
	}
	// Retain only a suffix that could be the beginning of a secret. This
	// keeps ordinary log writes flowing immediately while still protecting a
	// credential split across two writes.
	cut := len(combined)
	for _, secret := range r.base.values {
		maxPrefix := min(len(secret)-1, len(combined))
		for prefixLen := 1; prefixLen <= maxPrefix; prefixLen++ {
			if strings.HasSuffix(combined, secret[:prefixLen]) {
				cut = min(cut, len(combined)-prefixLen)
			}
		}
	}
	if formatStart := incompleteCredentialURLStart(combined); formatStart >= 0 {
		cut = min(cut, formatStart)
	}
	cut = r.base.completeSecretBoundary(combined, cut)
	r.pending = combined[cut:]
	return r.base.Redact(combined[:cut])
}

// Follow the replacer's leftmost, longest-first matches. A complete secret
// straddling cut must stay whole; overlapping matches must not make buffering
// grow across an arbitrarily long repeated value.
func (r *Redactor) completeSecretBoundary(value string, cut int) int {
	if cut == len(value) || r.matcher == nil {
		return cut
	}
	for offset := 0; offset < cut; {
		length := r.secretMatchLength(value[offset:])
		if offset+length > cut {
			return offset
		}
		offset += max(length, 1)
	}
	return cut
}

func (r *Redactor) secretMatchLength(value string) int {
	node := r.matcher
	length := 0
	for index := range len(value) {
		node = node.children[value[index]]
		if node == nil {
			break
		}
		length = max(length, node.length)
	}
	return length
}

// incompleteCredentialURLStart returns the start of a URL suffix that may
// still contain unredacted userinfo. Waiting for the terminating '@' prevents
// a password from escaping when the URL is split across writes.
func incompleteCredentialURLStart(value string) int {
	lower := strings.ToLower(value)
	if start := credentialURLStart(value, lower); start >= 0 {
		return start
	}
	if start := splitSchemeStart(value, lower); start >= 0 {
		return start
	}
	return splitAuthorizationStart(value, lower)
}

func credentialURLStart(value, lower string) int {
	start := max(strings.LastIndex(lower, "http://"), strings.LastIndex(lower, "https://"))
	if start < 0 {
		return -1
	}
	suffix := value[start:]
	if len(suffix) <= 512 && !strings.ContainsAny(suffix, " \t\r\n") &&
		!strings.Contains(suffix, "@") {
		return start
	}
	return -1
}

func splitSchemeStart(value, lower string) int {
	for _, scheme := range []string{"http://", "https://"} {
		for i := 1; i < len(scheme); i++ {
			if strings.HasSuffix(lower, scheme[:i]) {
				return len(value) - i
			}
		}
	}

	return -1
}

func splitAuthorizationStart(value, lower string) int {
	// Authorization headers can be split at any point, including between the
	// header name, scheme, and credential. Keep a bounded suffix until the
	// credential arrives so it cannot escape through a chunk boundary.
	if start := authorizationPrefixStart(value, lower, "authorization: bearer "); start >= 0 {
		return start
	}
	return authorizationPrefixStart(value, lower, "authorization: basic ")
}

func authorizationPrefixStart(value, lower, prefix string) int {
	for i := 1; i <= len(prefix); i++ {
		if strings.HasSuffix(lower, prefix[:i]) {
			return len(value) - i
		}
	}
	return -1
}

// Flush returns the final pending suffix, redacted as a complete fragment.
func (r *StreamingRedactor) Flush() string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	pending := r.pending
	r.pending = ""
	if r.base == nil {
		return pending
	}
	return r.base.Redact(pending)
}
