package redact

import (
	"regexp"
	"strings"
	"sync"
)

const formatCarryLimit = 512

var incompleteURL = regexp.MustCompile(`(?i)(https?://)[^\s/@?#]*\z`)

type Stream struct {
	mu        sync.Mutex
	base      *Redactor
	pending   []byte
	search    *matchNode
	lastMatch int
	anchors   int
	covered   int
	markerEnd int
	urlOpen   bool
	auth      authorizationState
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
	s.pending = nil
	s.search = nil
	s.lastMatch = 0
	s.anchors = 0
	s.covered = 0
	s.markerEnd = 0
	s.urlOpen = false
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
	frame := findURLFrame(value, s.urlOpen)
	urls := frame.matches
	headers, _ := scanAuthorization(value, len(value), s.auth)
	if frame.overflow {
		urls = append(urls, []int{frame.start, len(value) + 1, frame.start, frame.authority})
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
	s.urlOpen = frame.activeAfter(cut, len(value), s.urlOpen)
	s.auth = authAtCut
	return result
}

func (s *Stream) streamCut(value string, frame urlFrame, urls [][]int, final bool) int {
	if final {
		return len(value)
	}
	cut := s.base.secretPrefixBoundary(value)
	if start := partialSchemeStart(value, frame.continuation); start >= 0 {
		cut = min(cut, start)
	}
	if frame.start >= 0 && !frame.overflow {
		cut = min(cut, frame.start)
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
	headers [][]int,
) (authorizationState, [][]int) {
	authAtCut := s.auth
	for i := range cut {
		authAtCut.step(value[i])
		if s.base.matcher != nil && (value[i] == ':' || value[i] == '=') {
			s.anchors--
		}
	}
	s.lastMatch = max(0, s.lastMatch-cut)
	if s.auth.phase == authToken && len(headers) != 0 && headers[0][0] == 0 {
		continuation := headers[0][1]
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
		if chunk[i] == ':' || chunk[i] == '=' {
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
	return s.anchors == 0 && !s.urlOpen && s.auth.phase <= authAfterName
}

func (s *Stream) emitKnownSafe(chunk string) (string, bool) {
	size := len(s.pending) + len(chunk)
	if s.search.proper > size {
		return "", false
	}
	s.pending = append(s.pending, chunk...)
	cut := len(s.pending) - s.search.proper
	tailStart := max(0, len(s.pending)-len("http\u017f://")+1)
	if start := partialSchemeStart(string(s.pending[tailStart:]), 0); start >= 0 {
		cut = min(cut, tailStart+start)
	}
	for _, value := range s.pending[:cut] {
		s.auth.step(value)
	}
	result := string(s.pending[:cut])
	s.pending = s.pending[cut:]
	if len(s.pending) == 0 {
		s.pending = nil
	}
	if cap(s.pending) > 1024 && len(s.pending) < cap(s.pending)/4 {
		s.pending = append([]byte(nil), s.pending...)
	}
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
	s.pending = append(s.pending, chunk...)
	return true
}

func (s *Stream) holdPrefix(chunk string) bool {
	if s.search == nil || s.search.proper != len(s.pending)+len(chunk) {
		return false
	}
	s.pending = append(s.pending, chunk...)
	return true
}

func (s *Stream) retain(value string) {
	if value == "" {
		s.pending = nil
		return
	}
	if cap(s.pending) > 1024 && len(value) < cap(s.pending)/4 {
		s.pending = nil
	}
	s.pending = append(s.pending[:0], value...)
}

func formatMatches(value string, offset int) ([][]int, [][]int) {
	urls := credentialURL.FindAllStringSubmatchIndex(value[offset:], -1)
	headers := authorization.FindAllStringSubmatchIndex(value[offset:], -1)
	for _, matches := range [][][]int{urls, headers} {
		for _, match := range matches {
			for i := range match {
				match[i] += offset
			}
		}
	}
	return urls, headers
}

func formatPrefixBoundary(cut int, groups ...[][]int) int {
	for {
		previous := cut
		for _, matches := range groups {
			for _, match := range matches {
				if match[0] < cut && cut <= match[3] {
					cut = match[0]
				}
			}
		}
		if cut == previous {
			return cut
		}
	}
}

func authorizationEnd(value byte) bool {
	return value == ',' || formatSpace(value)
}

func formatSpace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\n' || value == '\r' || value == '\f'
}

type urlFrame struct {
	matches      [][]int
	continuation int
	start        int
	authority    int
	overflow     bool
}

func findURLFrame(value string, active bool) urlFrame {
	frame := urlFrame{start: -1}
	if active {
		for frame.continuation < len(value) && !urlAuthorityEnd(value[frame.continuation]) {
			frame.continuation++
		}
	}
	offset := frame.continuation
	frame.matches = credentialURL.FindAllStringSubmatchIndex(value[offset:], -1)
	for _, match := range frame.matches {
		for i := range match {
			match[i] += offset
		}
	}
	if match := incompleteURL.FindStringSubmatchIndex(value[offset:]); match != nil {
		frame.start = offset + match[0]
		frame.authority = offset + match[3]
		frame.overflow = len(value)-frame.start > formatCarryLimit
	}
	return frame
}

func (f urlFrame) activeAfter(cut, length int, active bool) bool {
	if f.overflow && cut > f.authority {
		return true
	}
	return active && (cut < f.continuation || (cut == f.continuation && f.continuation == length))
}

func urlAuthorityEnd(value byte) bool {
	return value == '@' || value == '/' || value == '?' || value == '#' || formatSpace(value)
}

func partialSchemeStart(value string, skip int) int {
	start := max(skip, len(value)-len("http\u017f://")+1)
	for i := start; i < len(value); i++ {
		tail := value[i:]
		for _, scheme := range []string{"http://", "https://", "http\u017f://"} {
			if len(tail) < len(scheme) && strings.EqualFold(tail, scheme[:len(tail)]) {
				return i
			}
		}
	}
	return -1
}
