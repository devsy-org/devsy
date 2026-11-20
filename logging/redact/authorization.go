package redact

import "strings"

type headerPhase uint8

const (
	headerSearch headerPhase = iota
	headerAfterName
	headerAfterQuotedName
	headerBeforeValue
	headerQuoted
	headerCompound
	headerAtom
	headerLine
	headerLineBreak
	headerOpaque
)

const maxHeaderDepth = 64

type credentialMaskStyle uint8

const (
	credentialText credentialMaskStyle = iota
	credentialInJSON
	credentialBareJSON
)

type credentialSpan struct {
	start int
	end   int
	style credentialMaskStyle
}

var (
	credentialFields = []string{
		"authorization",
		"proxy-authorization",
		"cookie",
		"set-cookie",
		"cookie2",
		"set-cookie2",
	}
	credentialNames = newAutomaton(credentialFields, false)
)

type authorizationState struct {
	name         *matchNode
	keyEscape    keyEscape
	phase        headerPhase
	quote        byte
	escaped      bool
	quotedKey    bool
	inQuote      bool
	quoteEscape  bool
	nameLength   int
	json         bool
	line         bool
	afterSpacing bool
	atom         [10]byte
	atomLength   int
	depth        uint
	objects      uint64
	masking      bool
}

type keyEscape struct {
	phase byte
	left  uint8
	code  uint16
}

func (e *keyEscape) next(value byte) (byte, bool) {
	switch e.phase {
	case '\\':
		if value == '\\' {
			return 0, false
		}
		if value == 'u' {
			*e = keyEscape{phase: 'u', left: 4}
			return 0, false
		}
		*e = keyEscape{}
		return escapedKeyByte(value), true
	case 'u':
		return e.unicodeByte(value)
	default:
		if value == '\\' {
			e.phase = '\\'
			return 0, false
		}
		return value, true
	}
}

func (e *keyEscape) unicodeByte(value byte) (byte, bool) {
	digit, ok := hexDigit(value)
	if !ok {
		*e = keyEscape{}
		return value, true
	}
	e.code = e.code<<4 | uint16(digit)
	e.left--
	if e.left != 0 {
		return 0, false
	}
	code := e.code
	*e = keyEscape{}
	if code > 127 {
		return 0, true
	}
	return byte(code), true
}

func escapedKeyByte(value byte) byte {
	switch value {
	case 't':
		return '\t'
	case 'n':
		return '\n'
	case 'r':
		return '\r'
	case 'f':
		return '\f'
	case '\\', '"', '\'', '/':
		return value
	default:
		return 0
	}
}

func hexDigit(value byte) (byte, bool) {
	value = asciiLower(value)
	switch {
	case value >= '0' && value <= '9':
		return value - '0', true
	case value >= 'a' && value <= 'f':
		return value - 'a' + 10, true
	default:
		return 0, false
	}
}

func asciiLower(value byte) byte {
	if value >= 'A' && value <= 'Z' {
		return value + ('a' - 'A')
	}
	return value
}

func isCredentialField(key string) bool {
	for _, field := range credentialFields {
		if strings.EqualFold(key, field) {
			return true
		}
	}
	return false
}

func (s *authorizationState) step(value byte) bool {
	if s.phase <= headerAfterQuotedName {
		s.readKey(value)
		s.masking = false
		return false
	}
	s.masking = s.readValue(value)
	return s.masking
}

func (s *authorizationState) readKey(raw byte) {
	if s.readRawQuote(raw) {
		return
	}
	escaped := s.keyEscape.phase != 0
	value, ready := s.keyEscape.next(raw)
	if !ready {
		return
	}
	if s.inQuote {
		s.nameLength++
	}
	if s.phase != headerSearch && s.readSeparator(value, escaped) {
		return
	}
	if s.name == nil {
		s.name = credentialNames
	}
	s.name = transition(credentialNames, s.name, asciiLower(value))
	s.phase = headerSearch
	s.line = false
	s.json = false
	if s.name.longest != 0 {
		s.phase = headerAfterName
	}
}

func (s *authorizationState) readRawQuote(raw byte) bool {
	if raw == '\\' {
		s.quoteEscape = !s.quoteEscape
		return false
	}
	escaped := s.quoteEscape
	s.quoteEscape = false
	if raw != '"' || escaped {
		return false
	}
	if s.inQuote && s.phase == headerAfterName && s.name.longest == s.nameLength {
		*s = authorizationState{phase: headerAfterQuotedName, json: true}
	} else {
		*s = authorizationState{inQuote: !s.inQuote}
	}
	return true
}

func (s *authorizationState) readSeparator(value byte, escaped bool) bool {
	if value == ':' || value == '=' {
		quoted := s.phase == headerAfterQuotedName
		*s = authorizationState{
			phase:     headerBeforeValue,
			quotedKey: quoted,
			inQuote:   s.inQuote,
			json:      s.inQuote || jsonFieldSeparator(value, s.json),
			line:      s.line || escaped || plainHeaderSeparator(value, quoted),
		}
		return true
	}
	if s.phase == headerAfterName && isAuthorizationQuote(value) {
		s.phase = headerAfterQuotedName
		s.line = escaped
		return true
	}
	return keySpacing(s.phase, value)
}

func keySpacing(phase headerPhase, value byte) bool {
	if phase == headerAfterQuotedName {
		return formatSpace(value)
	}
	return value == ' ' || value == '\t'
}

func jsonFieldSeparator(value byte, quoted bool) bool {
	return value == ':' && quoted
}

func plainHeaderSeparator(value byte, quoted bool) bool {
	return value == ':' && !quoted
}

func isAuthorizationQuote(value byte) bool {
	return value == '"' || value == '\''
}

func (s *authorizationState) readValue(value byte) bool {
	switch s.phase {
	case headerBeforeValue:
		return s.startValue(value)
	case headerQuoted:
		return s.readQuoted(value, headerSearch)
	case headerCompound:
		return s.readCompound(value)
	case headerAtom:
		return s.readAtom(value)
	case headerLine:
		return s.readLine(value)
	case headerLineBreak:
		return s.readContinuation(value)
	default:
		return true
	}
}

func (s *authorizationState) startValue(value byte) bool {
	if s.inQuote {
		return s.startEnclosedValue(value)
	}
	if s.line {
		return s.startLine(value)
	}
	return s.startDelimitedValue(value)
}

func (s *authorizationState) startDelimitedValue(value byte) bool {
	switch {
	case value == '\\':
		s.phase = headerLine
		return true
	case formatSpace(value):
		s.afterSpacing = true
	case isAuthorizationQuote(value):
		s.phase, s.quote = headerQuoted, value
	case value == '[' || value == '{':
		s.phase = headerCompound
		s.openCompound(value)
	default:
		return s.startAtom(value)
	}
	return false
}

func (s *authorizationState) startEnclosedValue(value byte) bool {
	if formatSpace(value) {
		return false
	}
	s.phase, s.quote = headerQuoted, '"'
	return s.readQuoted(value, headerSearch)
}

func (s *authorizationState) startLine(value byte) bool {
	if !s.quotedKey && isLineBreak(value) {
		s.phase = headerLineBreak
		return false
	}
	if formatSpace(value) {
		return false
	}
	s.phase = headerLine
	return true
}

func (s *authorizationState) startAtom(value byte) bool {
	if s.afterSpacing && !s.quotedKey {
		s.phase = headerLine
		return true
	}
	s.phase = headerAtom
	return s.readAtom(value)
}

func (s *authorizationState) readQuoted(value byte, resume headerPhase) bool {
	if s.escaped {
		s.escaped = false
		return true
	}
	if value == '\\' {
		s.escaped = true
		return true
	}
	if value == s.quote {
		if resume == headerSearch {
			*s = authorizationState{}
		} else {
			s.quote = 0
			s.phase = resume
		}
		return false
	}
	return true
}

func (s *authorizationState) openCompound(value byte) {
	if s.depth == maxHeaderDepth {
		s.phase = headerOpaque
		return
	}
	if value == '{' {
		s.objects |= uint64(1) << s.depth
	}
	s.depth++
}

func (s *authorizationState) readCompound(value byte) bool {
	if s.quote != 0 {
		return s.readQuoted(value, headerCompound)
	}
	switch value {
	case '"', '\'':
		s.quote = value
	case '{', '[':
		s.openCompound(value)
	case '}', ']':
		return s.closeCompound(value)
	case ',', ':':
	default:
		return !formatSpace(value)
	}
	return false
}

func (s *authorizationState) closeCompound(value byte) bool {
	bit := uint64(1) << (s.depth - 1)
	if (s.objects&bit != 0) != (value == '}') {
		s.phase = headerOpaque
		return true
	}
	s.objects &^= bit
	s.depth--
	if s.depth == 0 {
		*s = authorizationState{}
	}
	return false
}

func (s *authorizationState) readAtom(value byte) bool {
	if s.quotedKey {
		if isSerializedFieldEnd(value) {
			return s.finishValue(value)
		}
		return true
	}
	if formatSpace(value) {
		if s.atomLength <= len(s.atom) && knownHeaderScheme(string(s.atom[:s.atomLength])) {
			s.phase = headerLine
			return s.readLine(value)
		}
		return s.finishValue(value)
	}
	if s.atomLength < len(s.atom) {
		s.atom[s.atomLength] = value
	}
	s.atomLength = min(len(s.atom)+1, s.atomLength+1)
	return true
}

func knownHeaderScheme(value string) bool {
	for _, name := range []string{"basic", "bearer", "digest", "negotiate"} {
		if strings.EqualFold(name, value) {
			return true
		}
	}
	return false
}

func isSerializedFieldEnd(value byte) bool {
	return value == ',' || value == ']' || value == '}' || isLineBreak(value)
}

func isLineBreak(value byte) bool {
	return value == '\r' || value == '\n'
}

func (s *authorizationState) readLine(value byte) bool {
	if isLineBreak(value) {
		s.phase = headerLineBreak
		return false
	}
	return true
}

func (s *authorizationState) readContinuation(value byte) bool {
	switch value {
	case '\r', '\n':
		return false
	case ' ', '\t':
		s.phase = headerLine
		return true
	default:
		return s.finishValue(value)
	}
}

func (s *authorizationState) finishValue(value byte) bool {
	*s = authorizationState{}
	s.readKey(value)
	return false
}

func (s authorizationState) maskStyle() credentialMaskStyle {
	if !s.json {
		return credentialText
	}
	if s.quote == '"' {
		return credentialInJSON
	}
	return credentialBareJSON
}

func scanAuthorization(value string, initial authorizationState) []credentialSpan {
	state := initial
	var matches []credentialSpan
	span := credentialSpan{start: -1}
	for i := range len(value) {
		if state.step(value[i]) {
			if span.start < 0 {
				span = credentialSpan{start: i, style: state.maskStyle()}
			}
		} else if span.start >= 0 {
			span.end = i
			matches = append(matches, span)
			span.start = -1
		}
	}
	if span.start >= 0 {
		span.end = len(value)
		matches = append(matches, span)
	}
	return matches
}
