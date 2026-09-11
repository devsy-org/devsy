package secrets

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

const redactMask = "***"

var (
	credentialURLPattern = regexp.MustCompile(`(?i)(https?://)[^\s/@]+@`)
	authorizationPattern = regexp.MustCompile(`(?i)(authorization\s*[:=]\s*(?:bearer|basic)\s+)[^\s,]+`)
)

type Redactor struct {
	replacer  *strings.Replacer
	values    []string
	maxLength int
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

// StreamingRedactor preserves a short suffix between writes so secrets split
// across subprocess or logger chunks are still masked before they are
// forwarded. Call Flush when the stream ends to release the final suffix.
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
	r.pending = combined[cut:]
	return r.base.Redact(combined[:cut])
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
	if len(suffix) <= 512 && !strings.ContainsAny(suffix, " \t\r\n") && !strings.Contains(suffix, "@") {
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
