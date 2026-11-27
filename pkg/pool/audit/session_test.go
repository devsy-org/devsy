package audit

import (
	"strings"
	"testing"

	"github.com/devsy-org/devsy-signals/envelope"
	"github.com/devsy-org/devsy/pkg/pool/poolconfig"
	"github.com/devsy-org/devsy/pkg/pool/session"
	"github.com/stretchr/testify/require"
)

const (
	// maxLabelValue is the Kubernetes limit for the pool-session label.
	maxLabelValue = 63
	sessionSuffix = "aaaaaaaaaa"
	legacySession = "test-session"
)

func TestSessionIDsFromTheEngineAreAccepted(t *testing.T) {
	for _, pool := range []string{"a", "a-b", "team-a", "a1", strings.Repeat("a", 32)} {
		require.True(t, poolconfig.ValidPoolName(pool), pool)
		id, err := session.NewID(pool)
		require.NoError(t, err)
		require.NoError(
			t,
			checkSession(Event{Type: envelope.AuditSessionRequest, Session: id}, pool),
			id,
		)
		require.LessOrEqual(t, len(id), maxLabelValue, id)
	}
}

func TestSessionPatternAndEngineAgree(t *testing.T) {
	for _, id := range []string{
		"p-a-" + sessionSuffix,
		"p-" + strings.Repeat("a", 32) + "-" + sessionSuffix,
		"p-" + strings.Repeat("a", 33) + "-" + sessionSuffix,
		"p-a--" + sessionSuffix,
		"p--a-" + sessionSuffix,
		"p-a-" + sessionSuffix + "a",
		"p-a-aaaaaaaaa",
		"p-a-01aaaaaaaa",
		"p-A-" + sessionSuffix,
		"p-_a-" + sessionSuffix,
		"p-a_b-" + sessionSuffix,
		"p-a_-" + sessionSuffix,
		"s_01JD7K3X5Y8Q2W4E6R7T9ZABCD",
		legacySession,
		"",
	} {
		require.Equal(t, session.ValidID(id), sessionPattern.MatchString(id), id)
	}
}

func TestSessionMustBelongToTheWriterPool(t *testing.T) {
	cases := []struct{ pool, session string }{
		{"a", "p-b-" + sessionSuffix},
		{"a", "p-a-b-" + sessionSuffix},
		{"a-b", "p-a-" + sessionSuffix},
		{"a", "p-a-" + sessionSuffix + "a"},
		{"a", "p-a-aaaaaaaaa"},
		{"a", "p-a-01aaaaaaaa"},
		{"a", "p-A-" + sessionSuffix},
		{"a", "p-a-" + sessionSuffix + "\n"},
		{"a", " p-a-" + sessionSuffix},
		{"a", "s_01JD7K3X5Y8Q2W4E6R7T9ZABCD"},
		{"a", legacySession},
		{"a", "x"},
	}
	for _, c := range cases {
		err := checkSession(Event{Type: envelope.AuditSessionRequest, Session: c.session}, c.pool)
		require.Error(t, err, "%q in %s", c.session, c.pool)
		require.NotContains(t, err.Error(), c.session)
	}
	require.NoError(t, checkSession(Event{Type: envelope.AuditPolicyApply}, "a"))
}
