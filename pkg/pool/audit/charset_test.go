package audit

import (
	"strings"
	"testing"

	"github.com/devsy-org/devsy-signals/envelope"
	"github.com/stretchr/testify/suite"
)

type CharsetSuite struct {
	suite.Suite
}

func TestCharsetSuite(t *testing.T) {
	suite.Run(t, new(CharsetSuite))
}

func charsetEvent(data map[string]any) Event {
	return Event{Type: envelope.AuditSessionRequest, Data: data}
}

func (s *CharsetSuite) TestAcceptsNames() {
	cases := []map[string]any{
		{keyAgentName: "claude"},
		{keyAgentName: "a" + strings.Repeat("b", 62)},
		{keyProviderName: "docker-2.x_y"},
		{keyProviderName: "a" + strings.Repeat("b", 62)},
		{keyRepository: "devsy-org/devsy.git"},
		{keyRepository: strings.Repeat("a", 100) + "/" + strings.Repeat("b", 100)},
		{keyExecutable: "g++"},
		{keyExecutable: strings.Repeat("x", 128)},
		{keyRequesterKind: requesterLocalUser, keyUserID: "operator"},
		{keyRequesterKind: requesterLocalUser, keyUserID: "CORP\\jane.doe"},
		{keyRequesterKind: requesterLocalUser, keyUserID: "build$"},
		{keyRequesterKind: requesterLocalUser, keyUserID: strings.Repeat("u", 64)},
	}
	for _, data := range cases {
		s.NoError(checkCharsets(charsetEvent(data)), "%v", data)
	}
}

func (s *CharsetSuite) TestRejectsOutsideTheSet() {
	cases := []struct {
		key, value string
	}{
		{keyAgentName, "Claude"},
		{keyAgentName, "-claude"},
		{keyAgentName, "a b"},
		{keyAgentName, "a" + strings.Repeat("b", 63)},
		{keyAgentName, ""},
		{keyProviderName, "dock\ner"},
		{keyProviderName, "docker\n"},
		{keyProviderName, "a" + strings.Repeat("b", 63)},
		{keyProviderName, ""},
		{keyRepository, "owner"},
		{keyRepository, "a/b/c"},
		{keyRepository, "owner/na me"},
		{keyRepository, strings.Repeat("a", 101) + "/b"},
		{keyRepository, "a/" + strings.Repeat("b", 101)},
		{keyRepository, "/b"},
		{keyRepository, "a/"},
		{keyExecutable, "/usr/bin/devsy"},
		{keyExecutable, "devsy --token x"},
		{keyExecutable, strings.Repeat("x", 129)},
	}
	for _, c := range cases {
		err := checkCharsets(charsetEvent(map[string]any{c.key: c.value}))
		s.Require().Error(err, "%s %q", c.key, c.value)
		s.Contains(err.Error(), c.key)
		s.NotContains(err.Error(), strings.TrimSpace(c.value)+"x")
	}
}

func (s *CharsetSuite) TestLocalUserNeedsASafeName() {
	for _, id := range []string{
		"", "jane@example.com", "Jane Doe", "a/b", "-x", "$x", "a\\b\\c", "\\jane",
		"jane\x00", "jane\n", "j\u00e4ne", strings.Repeat("u", 65),
	} {
		err := checkCharsets(charsetEvent(map[string]any{
			keyRequesterKind: requesterLocalUser, keyUserID: id,
		}))
		s.Require().Error(err, "%q", id)
		s.Contains(err.Error(), keyUserID)
	}
}

func (s *CharsetSuite) TestAdapterUserIDIsLeftToTheEnvelope() {
	s.NoError(checkCharsets(charsetEvent(map[string]any{
		keyRequesterKind: "adapter", keyUserID: "claude:abc_123",
	})))
	s.NoError(checkCharsets(charsetEvent(map[string]any{keyUserID: "anything@x y"})))
}

func (s *CharsetSuite) TestIgnoresNonStringValues() {
	s.NoError(checkCharsets(charsetEvent(map[string]any{keyAgentName: 7})))
}
