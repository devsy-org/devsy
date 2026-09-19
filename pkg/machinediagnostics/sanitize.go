package machinediagnostics

import (
	"strings"
	"unicode/utf8"

	"github.com/devsy-org/devsy/pkg/secrets"
)

type Sanitizer struct{ redactor *secrets.Redactor }

func NewSanitizer(env []string) *Sanitizer {
	return &Sanitizer{redactor: secrets.NewEnvironmentRedactor(env)}
}
func (s *Sanitizer) Message(value string) string {
	value = s.redactor.Redact(value)
	value = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if r < 32 {
			return -1
		}
		return r
	}, value)
	if len(value) <= MaxEventMessageBytes {
		return value
	}
	cut := MaxEventMessageBytes
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut] + "…"
}
