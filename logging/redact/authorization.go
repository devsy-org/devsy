package redact

const (
	authName = iota
	authAfterName
	authBeforeScheme
	authScheme
	authLongS
	authNeedSpace
	authBeforeToken
	authToken
)

const authorizationName = "authorization"

var authorizationFailure = [...]int{0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0}

type authorizationState struct {
	phase        int
	position     int
	namePosition int
	scheme       string
}

func (s *authorizationState) restart(_ byte) {
	s.phase = authName
	s.position = 0
	s.scheme = ""
}

func asciiLower(value byte) byte {
	if value >= 'A' && value <= 'Z' {
		return value + ('a' - 'A')
	}
	return value
}

func (s *authorizationState) step(value byte) bool {
	if s.phase != authToken && s.nameByte(value) {
		s.phase = authAfterName
		s.position = 0
		s.scheme = ""
		return false
	}
	switch s.phase {
	case authName:
	case authAfterName:
		s.separatorByte(value)
	case authBeforeScheme:
		s.schemeStart(value)
	case authScheme:
		s.schemeByte(value)
	default:
		return s.tokenByte(value)
	}
	return false
}

func (s *authorizationState) nameByte(value byte) bool {
	value = asciiLower(value)
	for s.namePosition > 0 && authorizationName[s.namePosition] != value {
		s.namePosition = authorizationFailure[s.namePosition-1]
	}
	if authorizationName[s.namePosition] == value {
		s.namePosition++
	}
	if s.namePosition != len(authorizationName) {
		return false
	}
	s.namePosition = authorizationFailure[len(authorizationName)-1]
	return true
}

func (s *authorizationState) separatorByte(value byte) {
	if formatSpace(value) {
		return
	}
	if value == ':' || value == '=' {
		s.phase = authBeforeScheme
	} else {
		s.restart(value)
	}
}

func (s *authorizationState) schemeStart(value byte) {
	if formatSpace(value) {
		return
	}
	if asciiLower(value) == 'b' {
		s.phase = authScheme
		s.position = 1
	} else {
		s.restart(value)
	}
}

func (s *authorizationState) tokenByte(value byte) bool {
	switch s.phase {
	case authLongS:
		s.longSByte(value)
	case authNeedSpace:
		if formatSpace(value) {
			s.phase = authBeforeToken
		} else {
			s.restart(value)
		}
	case authBeforeToken:
		return s.tokenStart(value)
	case authToken:
		if authorizationEnd(value) {
			s.restart(value)
			return false
		}
		return true
	}
	return false
}

func (s *authorizationState) tokenStart(value byte) bool {
	if formatSpace(value) {
		return false
	}
	if value == ',' {
		s.restart(value)
		return false
	}
	s.phase = authToken
	s.namePosition = 0
	return true
}

func (s *authorizationState) longSByte(value byte) {
	if value != 0xbf {
		s.restart(value)
		return
	}
	s.phase = authScheme
	s.position++
	if s.position == len(s.scheme) {
		s.phase = authNeedSpace
	}
}

func (s *authorizationState) schemeByte(value byte) {
	if s.scheme == "" && !s.selectScheme(value) {
		return
	}
	if s.scheme[s.position] == 's' && value == 0xc5 {
		s.phase = authLongS
		return
	}
	if asciiLower(value) != s.scheme[s.position] {
		s.restart(value)
		return
	}
	s.position++
	if s.position == len(s.scheme) {
		s.phase = authNeedSpace
	}
}

func (s *authorizationState) selectScheme(value byte) bool {
	switch asciiLower(value) {
	case 'e':
		s.scheme = "bearer"
	case 'a':
		s.scheme = "basic"
	default:
		s.restart(value)
		return false
	}
	return true
}

func scanAuthorization(
	value string,
	limit int,
	initial authorizationState,
) [][]int {
	state := initial
	var matches [][]int
	start := -1
	for i := range limit {
		if state.step(value[i]) {
			if start < 0 {
				start = i
			}
		} else if start >= 0 {
			matches = append(matches, []int{start, i, start, start})
			start = -1
		}
	}
	if start >= 0 {
		matches = append(matches, []int{start, limit, start, start})
	}
	return matches
}
