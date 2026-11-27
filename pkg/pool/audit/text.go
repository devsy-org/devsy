package audit

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxWorkspaceBytes = 256
	maxActorIDRunes   = 128
	maxActorIDBytes   = maxActorIDRunes * utf8.UTFMax
)

// checkText applies the limits the control plane enforces on the envelope
// workspace and actor id, so the writer never stores a record the server
// refuses on upload. Errors name the field only.
func checkText(e Event) error {
	if !validText(e.Workspace, maxWorkspaceBytes) {
		return textError(e.Type, "workspace")
	}
	if utf8.RuneCountInString(e.Actor.ID) > maxActorIDRunes ||
		!validText(e.Actor.ID, maxActorIDBytes) {
		return textError(e.Type, "actor id")
	}
	return nil
}

// validText bounds the bytes of a free string and refuses characters that hide
// or reorder text: control characters, format characters such as bidi
// overrides and zero-width marks, and the line and paragraph separators.
func validText(text string, maxBytes int) bool {
	return len(text) <= maxBytes && !strings.ContainsFunc(text, hidden)
}

func hidden(r rune) bool {
	return unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp)
}

func textError(typ, field string) error {
	return fmt.Errorf("write audit event %s: %s is too long or has hidden characters", typ, field)
}
