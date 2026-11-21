package redact

import (
	"encoding/json"
	"regexp"
	"strings"
)

type authorizationFold uint8

const (
	noAuthorizationFold authorizationFold = iota
	emptyAuthorizationFold
	activeAuthorizationFold
)

var (
	authorizationSchemeStart = regexp.MustCompile(`(?i)^` + authorizationScheme)
	emptyAuthorizationAtEnd  = regexp.MustCompile(headerStart + `$`)
)

func (f authorizationFold) masks(line string) bool {
	if f == noAuthorizationFold {
		return false
	}
	trimmed := strings.TrimLeft(line, " \t")
	return trimmed != "" && (len(trimmed) != len(line) ||
		(f == emptyAuthorizationFold && authorizationSchemeStart.MatchString(trimmed)))
}

func (s *Stream) foldedRecord(record streamRecord) bool {
	followsCR := s.lastCR
	s.lastCR = record.delimiter == '\r'
	if followsCR && record.delimiter == '\n' && record.contentLength() == 0 {
		return false
	}
	if record.contentLength() > streamLineLimit {
		s.authorization = emptyAuthorizationFold
		return false
	}
	folded := s.authorization.masks(string(record.data))
	s.authorization = authorizationAtEnd(record.data)
	if folded && s.authorization == noAuthorizationFold {
		s.authorization = activeAuthorizationFold
	}
	return folded
}

func authorizationAtEnd(line []byte) authorizationFold {
	if !headerPattern.Match(line) || json.Valid(line) {
		return noAuthorizationFold
	}
	if emptyAuthorizationAtEnd.Match(line) {
		return emptyAuthorizationFold
	}
	return activeAuthorizationFold
}

func (s *Stream) maskFoldedRecord(line []byte) string {
	if json.Valid(line) {
		return s.base.jsonMask()
	}
	return s.base.Mask()
}
