package redact

import (
	"bytes"
	"net/url"
	"regexp"
	"strings"
	"sync"
)

const (
	formatCarryLimit     = 512
	pendingCapacityFloor = 1024
)

var incompleteURL = regexp.MustCompile(urlSchemePattern + urlAuthorityByte + `*\z`)

type Stream struct {
	mu              sync.Mutex
	base            *Redactor
	pending         []byte
	pendingCapacity int
	search          *matchNode
	lastMatch       int
	anchors         int
	covered         int
	markerEnd       int
	urlOpen         bool
	schemeStarted   bool
	auth            authorizationState
}

func NewStream(redactor *Redactor) *Stream {
	return &Stream{base: redactor}
}

func (s *Stream) RedactChunk(chunk string) string {
	if s == nil {
		return chunk
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.base == nil {
		return chunk
	}
	if chunk == "" {
		return ""
	}
	return s.processChunk(chunk)
}

func (s *Stream) Flush() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.base == nil {
		return ""
	}
	result := s.process(string(s.pending), true)
	s.resetPending()
	s.search = nil
	s.lastMatch = 0
	s.anchors = 0
	s.covered = 0
	s.markerEnd = 0
	s.urlOpen = false
	s.schemeStarted = false
	s.auth = authorizationState{}
	return result
}

func (s *Stream) processChunk(chunk string) string {
	s.trackChunk(chunk)
	if s.holdPrefix(chunk) {
		return ""
	}
	if len(s.pending) == 0 {
		return s.process(chunk, false)
	}
	if s.canEmitKnownSafe() {
		if result, ok := s.emitKnownSafe(chunk); ok {
			return result
		}
	}
	if s.deferUnresolved(chunk) {
		return ""
	}
	return s.process(string(s.pending)+chunk, false)
}

func (s *Stream) process(value string, final bool) string {
	frame := findURLFrame(value, s.urlOpen, final, s.schemeStarted)
	urls := frame.matches
	headers := scanAuthorization(value, s.auth)
	maskingEnd := frame.maskingEnd(value, final)
	if maskingEnd > frame.authority {
		urls = append(urls, []int{frame.start, maskingEnd + 1, frame.start, frame.authority})
	}
	cut := s.streamCut(value, frame, urls, final)
	s.advanceURL(frame)
	authAtCut, headers := s.advanceAuthorization(value, cut, headers)
	result, covered, markerEnd := s.base.known(value, matchState{
		cut: cut, covered: s.covered, markerEnd: s.markerEnd, urls: urls, headers: headers,
	})
	s.retain(value[cut:])
	s.covered = covered
	s.markerEnd = markerEnd
	s.urlOpen = frame.activeAfter(cut, maskingEnd, s.urlOpen)
	s.auth = authAtCut
	return result
}

func (s *Stream) streamCut(value string, frame urlFrame, urls [][]int, final bool) int {
	if final {
		return len(value)
	}
	cut := s.base.secretPrefixBoundary(value)
	if start := partialSchemeStart(value, frame.skip, s.schemeStarted); start >= 0 {
		cut = min(cut, start)
	}
	if frame.start >= 0 {
		cut = frame.cutAuthorityPrefix(cut, len(value))
	}
	if frame.hold >= 0 {
		cut = min(cut, frame.hold)
	}
	return formatPrefixBoundary(cut, urls)
}

func (s *Stream) advanceURL(frame urlFrame) {
	if s.urlOpen {
		s.covered = max(s.covered, frame.continuation)
		s.markerEnd = max(s.markerEnd, frame.continuation)
	}
}

func (s *Stream) advanceAuthorization(
	value string,
	cut int,
	headers []credentialSpan,
) (authorizationState, []credentialSpan) {
	authAtCut := s.auth
	for i := range cut {
		authAtCut.step(value[i])
		s.schemeStarted = stepSchemeName(s.schemeStarted, value[i])
		if s.base.matcher != nil && headerAnchor(value[i]) {
			s.anchors--
		}
	}
	s.lastMatch = max(0, s.lastMatch-cut)
	if s.auth.masking && len(headers) != 0 && headers[0].start == 0 {
		continuation := headers[0].end
		s.covered = max(s.covered, continuation)
		s.markerEnd = max(s.markerEnd, continuation)
		headers = headers[1:]
	}
	return authAtCut, headers
}

func (s *Stream) trackChunk(chunk string) {
	if s.base.matcher == nil {
		return
	}
	root := s.base.matcher.forward
	if s.search == nil {
		s.search = root
	}
	offset := len(s.pending)
	for i := range len(chunk) {
		if headerAnchor(chunk[i]) {
			s.anchors++
		}
		s.search = transition(root, s.search, chunk[i])
		if s.search.longest != 0 {
			s.lastMatch = offset + i + 1
		}
	}
}

func (s *Stream) canEmitKnownSafe() bool {
	return s.noKnownMatchPending() && s.noFormatRisk()
}

func (s *Stream) noKnownMatchPending() bool {
	return s.search != nil && s.lastMatch == 0 && s.covered == 0 && s.markerEnd == 0
}

func (s *Stream) noFormatRisk() bool {
	return s.anchors == 0 && !s.urlOpen && s.auth.phase <= headerAfterName &&
		!s.auth.inQuote && s.auth.keyEscape.phase == 0
}

func (s *Stream) emitKnownSafe(chunk string) (string, bool) {
	size := len(s.pending) + len(chunk)
	if s.search.proper > size {
		return "", false
	}
	s.appendPending(chunk)
	cut := len(s.pending) - s.search.proper
	tailStart := max(0, len(s.pending)-len("http\u017f://")+1)
	if start := partialSchemeStart(string(s.pending[tailStart:]), 0, s.schemeStarted); start >= 0 {
		cut = min(cut, tailStart+start)
	}
	for _, value := range s.pending[:cut] {
		s.auth.step(value)
		s.schemeStarted = stepSchemeName(s.schemeStarted, value)
	}
	result := string(s.pending[:cut])
	s.pending = s.pending[cut:]
	s.compactPending()
	return result, true
}

func (s *Stream) deferUnresolved(chunk string) bool {
	if s.search == nil || s.search.proper < 4*formatCarryLimit {
		return false
	}
	tail := s.search.proper
	size := len(s.pending) + len(chunk)
	if tail > size || size-tail >= tail/2 || len(chunk) >= tail/2 {
		return false
	}
	s.appendPending(chunk)
	return true
}

func (s *Stream) holdPrefix(chunk string) bool {
	if s.search == nil || s.search.proper != len(s.pending)+len(chunk) {
		return false
	}
	s.appendPending(chunk)
	return true
}

func (s *Stream) retain(value string) {
	if value == "" {
		s.resetPending()
		return
	}
	if s.shouldCompactPending(len(value)) {
		s.resetPending()
	}
	s.pending = s.pending[:0]
	s.appendPending(value)
}

func (s *Stream) appendPending(value string) {
	available := cap(s.pending) - len(s.pending)
	s.pending = append(s.pending, value...)
	if len(value) > available {
		s.pendingCapacity = cap(s.pending)
	}
}

func (s *Stream) compactPending() {
	if len(s.pending) == 0 {
		s.resetPending()
		return
	}
	if s.shouldCompactPending(len(s.pending)) {
		s.pending = bytes.Clone(s.pending)
		s.pendingCapacity = cap(s.pending)
	}
}

func (s *Stream) shouldCompactPending(length int) bool {
	return s.pendingCapacity > pendingCapacityFloor && length < s.pendingCapacity/4
}

func (s *Stream) resetPending() {
	s.pending = nil
	s.pendingCapacity = 0
}

func formatMatches(value string) ([][]int, []credentialSpan) {
	return urlMatches(value, 0), scanAuthorization(value, authorizationState{})
}

func urlMatches(value string, offset int) [][]int {
	matches := urlAuthority.FindAllStringSubmatchIndex(value[offset:], -1)
	kept := matches[:0]
	for _, match := range matches {
		last := offset + match[1] - 1
		if value[last] == '"' || value[last] == '\\' {
			authority := trimURLPunctuation(value[offset+match[3] : last])
			if !malformedURLAuthority(authority) {
				continue
			}
			match[1] = match[3] + len(authority) + 1
		}
		for i := range match {
			match[i] += offset
		}
		kept = append(kept, match)
	}
	return kept
}

func formatPrefixBoundary(cut int, groups ...[][]int) int {
	for {
		previous := cut
		for _, matches := range groups {
			for _, match := range matches {
				if match[0] < cut && cut <= match[3] {
					cut = min(cut, schemeCarryStart(match[0], match[3]))
				}
			}
		}
		if cut == previous {
			return cut
		}
	}
}

func schemeCarryStart(start, authority int) int {
	if authority-start <= len("http\u017f://") {
		return start
	}
	return authority - len("://")
}

func headerAnchor(value byte) bool {
	return value == ':' || value == '=' || value == '\\' || value == '"'
}

func formatSpace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\n' || value == '\r' || value == '\f'
}

type urlFrame struct {
	matches      [][]int
	continuation int
	skip         int
	hold         int
	start        int
	authority    int
	overflow     bool
	continued    bool
}

func findURLFrame(value string, active, final, schemeStarted bool) urlFrame {
	frame := urlFrame{start: -1, hold: -1}
	if active {
		frame.continueAuthority(value, final)
	}
	input, bias := value, 0
	if !active && schemeStarted && needsInitialSchemeLetter(value) {
		input = "x" + value
		bias = 1
	}
	length := urlScanLimit(value, final)
	frame.matches = urlMatches(input[:length+bias], frame.skip)
	if bias != 0 {
		shiftURLMatches(frame.matches, bias)
	}
	frame.findIncomplete(value[:length], input[:length+bias], bias, final)
	if length < len(value) && frame.overflow {
		frame.hold = minNonnegative(frame.hold, length)
	}
	return frame
}

func (f *urlFrame) continueAuthority(value string, final bool) {
	f.skip, f.continued, f.hold = continuationBoundary(value, final)
	f.continuation = f.skip
	lastAt := strings.LastIndexByte(value[:f.skip], '@')
	if lastAt < 0 || (f.continued && f.skip-lastAt > formatCarryLimit) {
		return
	}
	f.continuation = lastAt
	if f.continued {
		f.hold = lastAt
	}
}

func continuationBoundary(value string, final bool) (int, bool, int) {
	end := urlAuthorityLength(value)
	if final || end != urlScanLimit(value, final) {
		return end, false, -1
	}
	if end < len(value) {
		return end, true, end
	}
	return end, true, -1
}

func urlScanLimit(value string, final bool) int {
	if final {
		return len(value)
	}
	return incompleteURLEscapeStart(value)
}

func shiftURLMatches(matches [][]int, bias int) {
	for _, match := range matches {
		for i := range match {
			match[i] = max(0, match[i]-bias)
		}
	}
}

func (f *urlFrame) findIncomplete(value, input string, bias int, final bool) {
	match := incompleteURL.FindStringSubmatchIndex(input[f.skip:])
	if match == nil {
		return
	}
	f.start = max(0, f.skip+match[0]-bias)
	f.authority = f.skip + match[3] - bias
	f.overflow = len(value)-f.start > formatCarryLimit
	if f.overflow && !final {
		lastAt := strings.LastIndexByte(value[f.authority:], '@')
		if lastAt >= 0 && len(value)-f.authority-lastAt <= formatCarryLimit {
			f.hold = f.authority + lastAt
		}
	}
}

func (f urlFrame) cutAuthorityPrefix(cut, length int) int {
	if !f.overflow {
		return min(cut, f.start)
	}
	if f.authority == length {
		return min(cut, max(f.start, f.authority-3))
	}
	return cut
}

func (f urlFrame) maskingEnd(value string, final bool) int {
	if f.overflow {
		if f.hold >= 0 {
			return f.hold
		}
		if final {
			if lastAt := strings.LastIndexByte(value[f.authority:], '@'); lastAt >= 0 {
				return f.authority + lastAt
			}
		}
		return len(strings.TrimRight(value, "\\"))
	}
	if !final || f.start < 0 {
		return -1
	}
	authority := trimURLPunctuation(value[f.authority:])
	if malformedURLAuthority(authority) {
		return f.authority + len(authority)
	}
	return -1
}

func malformedURLAuthority(authority string) bool {
	if !strings.Contains(authority, ":") {
		return false
	}
	parsed, err := url.Parse("http://" + authority)
	return err != nil ||
		(!strings.HasPrefix(authority, "[") && strings.Contains(parsed.Hostname(), ":"))
}

func trimURLPunctuation(value string) string {
	tail := strings.TrimRight(value, "\"')}>],;.!\\")
	if strings.HasPrefix(value, "[") {
		if bracket := strings.IndexByte(value, ']'); bracket >= len(tail) {
			return value[:bracket+1]
		}
	}
	return tail
}

func (f urlFrame) activeAfter(cut, maskingEnd int, active bool) bool {
	if f.overflow && cut > f.authority {
		return cut <= maskingEnd
	}
	return active && (cut < f.continuation || f.continued)
}

const serializedURLEscapeLength = len(`\u0000`)

func urlAuthorityLength(value string) int {
	for i := 0; i < len(value); i++ {
		if value[i] == '\\' && i+serializedURLEscapeLength <= len(value) &&
			isURLEscapePrefix(value[i:i+serializedURLEscapeLength]) {
			i += serializedURLEscapeLength - 1
			continue
		}
		if urlAuthorityEnd(value[i]) {
			return i
		}
	}
	return len(value)
}

func incompleteURLEscapeStart(value string) int {
	start := max(0, len(value)-serializedURLEscapeLength+1)
	if slash := strings.LastIndexByte(value[start:], '\\'); slash >= 0 {
		slash += start
		if isURLEscapePrefix(value[slash:]) {
			return slash
		}
	}
	return len(value)
}

func isURLEscapePrefix(value string) bool {
	if len(value) == 1 {
		return value[0] == '\\'
	}
	if !strings.HasPrefix(value, `\u`) {
		return false
	}
	for i := 2; i < len(value); i++ {
		if _, ok := hexDigit(value[i]); !ok {
			return false
		}
	}
	return true
}

func minNonnegative(value, other int) int {
	if value < 0 {
		return other
	}
	return min(value, other)
}

func urlAuthorityEnd(value byte) bool {
	switch value {
	case '/', '?', '#', '"', '\\':
		return true
	default:
		return formatSpace(value)
	}
}

func partialSchemeStart(value string, skip int, priorScheme bool) int {
	start := max(skip, len(value)-len("http\u017f://")+1)
	separator := partialURLSeparator(value, skip, priorScheme)
	for i := start; i < len(value); i++ {
		if separator == i {
			return i
		}
		tail := value[i:]
		for _, scheme := range []string{"http://", "https://", "http\u017f://"} {
			if len(tail) < len(scheme) && strings.EqualFold(tail, scheme[:len(tail)]) {
				return i
			}
		}
	}
	return separator
}

func partialURLSeparator(value string, skip int, priorScheme bool) int {
	for _, suffix := range []string{":", ":/"} {
		if !strings.HasSuffix(value[skip:], suffix) {
			continue
		}
		start := len(value) - len(suffix)
		i := start
		for i > skip && schemeNameByte(value[i-1]) {
			i--
			if asciiLetter(value[i]) {
				return start
			}
		}
		if i == 0 && priorScheme {
			return start
		}
	}
	return -1
}

func needsInitialSchemeLetter(value string) bool {
	for i := range len(value) {
		if value[i] == ':' {
			return strings.HasPrefix(value[i:], "://")
		}
		if !schemeNameByte(value[i]) || asciiLetter(value[i]) {
			return false
		}
	}
	return false
}

func stepSchemeName(started bool, value byte) bool {
	return asciiLetter(value) || (started && schemeNameByte(value))
}

func schemeNameByte(value byte) bool {
	return asciiLetter(value) || value >= '0' && value <= '9' || value == '+' || value == '.' ||
		value == '-'
}

func asciiLetter(value byte) bool {
	return asciiLower(value) >= 'a' && asciiLower(value) <= 'z'
}
