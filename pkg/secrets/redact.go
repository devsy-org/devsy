package secrets

import (
	"encoding/json"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

const (
	redactMask     = "***"
	jsonRedactMask = `"***"`
)

var (
	credentialURLPattern = regexp.MustCompile(`(?i)(https?://)[^\s/@]+@`)
	authorizationPattern = regexp.MustCompile(
		`(?i)(authorization\s*[:=]\s*(?:bearer|basic)\s+)[^\s,]+`,
	)
	jsonScalarLinePattern = regexp.MustCompile(
		`("(?:[^"\\]|\\.)+"\s*:\s*)(-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?|true|false|null)`,
	)
)

type Redactor struct {
	replacer  *strings.Replacer
	values    []string
	entries   []string
	lines     map[string]string
	scalars   *strings.Replacer
	maxLength int
	matcher   *secretNode
}

// NewRedactor masks the values (not keys) of KEY=VALUE entries; empty values are ignored.
func NewRedactor(secretsEnv []string) *Redactor {
	values := make([]string, 0, len(secretsEnv))
	replacements := map[string]string{}
	lines := map[string]string{}
	scalarValues := map[string]struct{}{}
	var entries []string
	for _, entry := range secretsEnv {
		_, value, ok := strings.Cut(entry, "=")
		if !ok || value == "" {
			continue
		}
		entries = append(entries, entry)
		values = appendPlainCandidates(values, replacements, value)
		fragments, lineValues := jsonScalarFragments(value, scalarValues)
		maps.Copy(lines, lineValues)
		values = appendMappedCandidates(values, replacements, fragments)
	}
	if len(values) == 0 {
		return &Redactor{}
	}

	// Mask longer values first so an overlapping prefix (e.g. "sec" of "secret")
	// cannot partially match ahead of the full value.
	sort.SliceStable(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	pairs := make([]string, 0, len(values)*2)
	for _, v := range values {
		pairs = append(pairs, v, replacements[v])
	}
	var scalarReplacer *strings.Replacer
	if len(scalarValues) > 0 {
		var scalarKeys []string
		for scalar := range scalarValues {
			scalarKeys = append(scalarKeys, scalar)
		}
		sort.Slice(
			scalarKeys,
			func(i, j int) bool { return len(scalarKeys[i]) > len(scalarKeys[j]) },
		)
		var scalarPairs []string
		for _, scalar := range scalarKeys {
			scalarPairs = append(scalarPairs, scalar, redactMask)
		}
		scalarReplacer = strings.NewReplacer(scalarPairs...)
	}

	return &Redactor{
		replacer:  strings.NewReplacer(pairs...),
		values:    values,
		entries:   entries,
		lines:     lines,
		scalars:   scalarReplacer,
		maxLength: len(values[0]),
		matcher:   newSecretMatcher(values),
	}
}

func appendPlainCandidates(values []string, replacements map[string]string, value string) []string {
	for _, candidate := range redactionValues(value) {
		if _, ok := replacements[candidate]; !ok {
			values = append(values, candidate)
		}
		replacements[candidate] = redactMask
	}
	return values
}

func appendMappedCandidates(values []string, replacements, fragments map[string]string) []string {
	for candidate, replacement := range fragments {
		if _, ok := replacements[candidate]; !ok {
			values = append(values, candidate)
			replacements[candidate] = replacement
		}
	}
	return values
}

func jsonScalarFragments(
	value string,
	scalarValues map[string]struct{},
) (map[string]string, map[string]string) {
	fragments := map[string]string{}
	lines := map[string]string{}
	decoded, ok := decodeMultilineJSON(value)
	if !ok {
		return fragments, lines
	}
	addJSONScalarValues(lines, fragments, scalarValues, decoded)
	if compact, err := json.Marshal(decoded); err == nil {
		switch decoded.(type) {
		case []any, map[string]any:
			fragments[string(compact)] = jsonRedactMask
		}
	}
	addJSONScalarLineFragments(fragments, lines, value)
	return fragments, lines
}

func decodeMultilineJSON(value string) (any, bool) {
	if !strings.ContainsAny(value, "\r\n") || !json.Valid([]byte(value)) {
		return nil, false
	}
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, false
	}
	return decoded, true
}

func addJSONScalarLineFragments(fragments, lines map[string]string, value string) {
	for _, match := range jsonScalarLinePattern.FindAllStringSubmatch(value, -1) {
		key := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(match[1]), ":"))
		for _, prefix := range []string{match[1], key + ":"} {
			candidate := prefix + match[2]
			replacement := prefix + jsonRedactMask
			lines[candidate] = replacement
			addBoundedJSONFragments(fragments, candidate, replacement)
		}
	}
}

func addBoundedJSONFragments(fragments map[string]string, candidate, replacement string) {
	for _, suffix := range []string{",", "}", "]"} {
		for _, space := range []string{"", " ", "\t"} {
			bounded := candidate + space + suffix
			fragments[bounded] = replacement + space + suffix
			encoded, err := json.Marshal(bounded)
			if err == nil && len(encoded) > 2 {
				fragments[string(encoded[1:len(encoded)-1])] = redactMask + space + suffix
			}
		}
	}
}

func addJSONScalarValues(
	lines, fragments map[string]string,
	scalarValues map[string]struct{},
	value any,
) {
	switch value := value.(type) {
	case json.Number:
		lines[value.String()] = jsonRedactMask
		scalarValues[value.String()] = struct{}{}
	case bool:
		lines[strconv.FormatBool(value)] = jsonRedactMask
		scalarValues[strconv.FormatBool(value)] = struct{}{}
	case nil:
		lines["null"] = jsonRedactMask
		scalarValues["null"] = struct{}{}
	case string:
		addJSONScalarString(lines, fragments, scalarValues, value)
	case []any:
		addJSONScalarEntries(lines, fragments, scalarValues, value)
	case map[string]any:
		addJSONScalarEntries(lines, fragments, scalarValues, slices.Collect(maps.Values(value)))
	}
}

func addJSONScalarEntries(
	lines, fragments map[string]string,
	scalarValues map[string]struct{},
	entries []any,
) {
	for _, entry := range entries {
		addJSONScalarValues(lines, fragments, scalarValues, entry)
	}
}

func addJSONScalarString(
	lines, fragments map[string]string,
	scalarValues map[string]struct{},
	value string,
) {
	if isJSONScalarToken(value) {
		scalarValues[value] = struct{}{}
		lines[value] = jsonRedactMask
		encoded, _ := json.Marshal(value)
		fragments[string(encoded)] = jsonRedactMask
	}
}

func redactionValues(value string) []string {
	values := []string{value}
	if decoded, ok := decodeMultilineJSON(value); ok {
		values = append(values, jsonStringValues(decoded)...)
	} else {
		values = append(values, textSecretFragments(value)...)
	}
	for _, candidate := range append([]string(nil), values...) {
		encoded, err := json.Marshal(candidate)
		if err == nil && len(encoded) > 2 {
			values = append(values, string(encoded[1:len(encoded)-1]))
		}
	}
	return values
}

func textSecretFragments(value string) []string {
	var values []string
	for _, fragment := range strings.FieldsFunc(value, func(r rune) bool { return r == '\n' || r == '\r' }) {
		if hasRedactableContent(fragment) {
			values = append(values, fragment)
		}
	}
	return values
}

func jsonStringValues(value any) []string {
	var values []string
	switch value := value.(type) {
	case string:
		if hasRedactableContent(value) && !isJSONScalarToken(value) {
			values = append(values, value)
		}
	case []any:
		for _, entry := range value {
			values = append(values, jsonStringValues(entry)...)
		}
	case map[string]any:
		for _, entry := range value {
			values = append(values, jsonStringValues(entry)...)
		}
	}
	return values
}

func isJSONScalarToken(value string) bool {
	if !json.Valid([]byte(value)) {
		return false
	}
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return false
	}
	switch decoded.(type) {
	case json.Number, bool, nil:
		return true
	default:
		return false
	}
}

func hasRedactableContent(value string) bool {
	return strings.IndexFunc(value, unicode.IsLetter) >= 0 ||
		strings.IndexFunc(value, unicode.IsNumber) >= 0
}

// Combine returns a redactor that masks the values known by every input.
// Nil redactors are ignored.
func Combine(redactors ...*Redactor) *Redactor {
	entries := make([]string, 0)
	for _, redactor := range redactors {
		if redactor == nil {
			continue
		}
		entries = append(entries, redactor.entries...)
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
	return r.redact(s, true)
}

func (r *Redactor) redact(s string, standalone bool) string {
	if r != nil {
		s = r.redactKnown(s, standalone)
	}
	s = credentialURLPattern.ReplaceAllString(s, "$1***@")
	return authorizationPattern.ReplaceAllString(s, "$1***")
}

func (r *Redactor) redactKnown(s string, standalone bool) string {
	if standalone {
		trimmed := strings.TrimSpace(s)
		scalar := strings.TrimSpace(strings.TrimSuffix(trimmed, ","))
		if replacement, ok := r.lines[scalar]; ok {
			s = strings.Replace(s, scalar, replacement, 1)
		}
	}
	if r.replacer != nil {
		s = r.replacer.Replace(s)
	}
	if standalone && r.scalars != nil {
		s = redactScalarContent(s, r.scalars)
	}
	return s
}

func redactScalarContent(text string, replacer *strings.Replacer) string {
	if !json.Valid([]byte(text)) {
		return replacer.Replace(text)
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return jsonRedactMask
	}
	redacted, changed := redactJSONStringValues(decoded, replacer)
	if !changed {
		return text
	}
	encoded, err := json.Marshal(redacted)
	if err != nil {
		return jsonRedactMask
	}
	return string(encoded)
}

func redactJSONStringValues(value any, replacer *strings.Replacer) (any, bool) {
	switch value := value.(type) {
	case string:
		redacted := replacer.Replace(value)
		return redacted, redacted != value
	case []any:
		return redactJSONArray(value, replacer)
	case map[string]any:
		return redactJSONObject(value, replacer)
	default:
		return value, false
	}
}

func redactJSONArray(value []any, replacer *strings.Replacer) (any, bool) {
	changed := false
	for i, entry := range value {
		var entryChanged bool
		value[i], entryChanged = redactJSONStringValues(entry, replacer)
		changed = changed || entryChanged
	}
	return value, changed
}

func redactJSONObject(value map[string]any, replacer *strings.Replacer) (any, bool) {
	redacted := make(map[string]any, len(value))
	changed := false
	for key, entry := range value {
		redactedKey := replacer.Replace(key)
		redactedValue, entryChanged := redactJSONStringValues(entry, replacer)
		redacted[redactedKey] = redactedValue
		changed = changed || entryChanged || redactedKey != key
	}
	return redacted, changed
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
	return r.base.redact(combined[:cut], false)
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
	return r.base.redact(pending, false)
}
