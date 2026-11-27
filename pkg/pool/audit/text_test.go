package audit

import (
	"strings"
	"testing"

	"github.com/devsy-org/devsy-signals/envelope"
	"github.com/stretchr/testify/suite"
)

type TextSuite struct {
	suite.Suite
}

func TestTextSuite(t *testing.T) {
	suite.Run(t, new(TextSuite))
}

func textEvent(workspace, actorID string) Event {
	return Event{
		Type:      envelope.AuditSessionRequest,
		Workspace: workspace,
		Actor:     envelope.Actor{Kind: envelope.ActorAgent, ID: actorID},
	}
}

// hiddenSamples cover Cc, Cf, Zl and Zp, including the edges of each range.
var hiddenSamples = []string{
	"\x00", "\x1f", "\x7f", "\u0080", "\u0085", "\u009f", "\n", "\r", "\t",
	"\u00ad", "\u061c", "\u200b", "\u200d", "\u200e", "\u202a", "\u202e",
	"\u2060", "\u2066", "\u2069", "\ufeff", "\U000e0001",
	"\u2028", "\u2029",
}

func (s *TextSuite) TestAcceptsOrdinaryText() {
	for _, v := range []string{
		"", "ws-1", "my workspace", "a\u00a0b", "a\u3000b", "j\u00e4ne", "\u4e16\u754c", "\U0001f600",
	} {
		s.NoError(checkText(textEvent(v, "actor")), "workspace %q", v)
		s.NoError(checkText(textEvent("", "a"+v)), "actor %q", v)
	}
}

func (s *TextSuite) TestWorkspaceLimitIsInBytes() {
	s.NoError(checkText(textEvent(strings.Repeat("w", 256), "a")))
	s.NoError(checkText(textEvent(strings.Repeat("\u00e9", 128), "a")))
	s.Error(checkText(textEvent(strings.Repeat("w", 257), "a")))
	s.Error(checkText(textEvent(strings.Repeat("\u00e9", 129), "a")))
	s.Error(checkText(textEvent(strings.Repeat("w", 255)+"\u00e9", "a")))
}

func (s *TextSuite) TestActorIDLimitIsInCharacters() {
	s.NoError(checkText(textEvent("", strings.Repeat("a", 128))))
	s.NoError(checkText(textEvent("", strings.Repeat("\u00e9", 128))))
	s.NoError(checkText(textEvent("", strings.Repeat("\U0001f600", 128))))
	s.Error(checkText(textEvent("", strings.Repeat("a", 129))))
	s.Error(checkText(textEvent("", strings.Repeat("\u00e9", 129))))
	s.Error(checkText(textEvent("", strings.Repeat("\U0001f600", 129))))
}

func (s *TextSuite) TestRefusesHiddenCharacters() {
	for _, h := range hiddenSamples {
		for _, v := range []string{h, "a" + h, h + "a", "a" + h + "b"} {
			err := checkText(textEvent(v, "actor"))
			s.Require().Error(err, "workspace %q", v)
			s.Contains(err.Error(), "workspace")
			err = checkText(textEvent("", v))
			s.Require().Error(err, "actor %q", v)
			s.Contains(err.Error(), "actor id")
		}
	}
}

func (s *TextSuite) TestErrorsNeverEchoTheValue() {
	const canary = "canary-value-"
	err := checkText(textEvent(canary+"\u202e", "a"))
	s.Require().Error(err)
	s.NotContains(err.Error(), canary)
	err = checkText(textEvent("", canary+strings.Repeat("a", 128)))
	s.Require().Error(err)
	s.NotContains(err.Error(), canary)
}
