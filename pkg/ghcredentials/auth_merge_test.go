package ghcredentials

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func authFixture(user, token string) string {
	if user == "" {
		return ""
	}
	return "github.example:\n  git_protocol: https\n  user: " + user + "\n" +
		"  oauth_token: " + token + "\n  users:\n    " + user + ":\n      oauth_token: " + token + "\n"
}

func TestHostAuthenticationMergeNeverSplicesOrResurrects(t *testing.T) {
	alice := authFixture("alice", "dummy-alice")
	refresh := authFixture("alice", "dummy-refreshed-alice")
	bob := authFixture("bob", "dummy-bob")
	for _, tc := range []struct {
		name, base, desired, latest, expected string
		conflict                              bool
	}{
		{"unchanged", alice, alice, bob, bob, false},
		{"uncontested refresh", alice, refresh, alice, refresh, false},
		{"idempotent refresh", alice, refresh, refresh, refresh, false},
		{"refresh after switch", alice, refresh, bob, bob, true},
		{"switch after refresh", alice, bob, refresh, refresh, true},
		{"refresh after logout", alice, refresh, "", "", true},
		{"logout after refresh", alice, "", refresh, refresh, true},
		{"login after logout", alice, bob, "", "", true},
		{"logout after login", alice, "", bob, bob, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := mergeHosts([]byte(tc.base), []byte(tc.desired), []byte(tc.latest))
			if tc.conflict {
				require.ErrorIs(t, err, errAuthConflict)
			} else {
				require.NoError(t, err)
			}
			n, err := decode(out)
			require.NoError(t, err)
			expected, err := decode([]byte(tc.expected))
			require.NoError(t, err)
			require.True(
				t,
				equal(authentication(get(n, testHost)), authentication(get(expected, testHost))),
				"authentication identity was spliced or resurrected",
			)
		})
	}
}

func TestHostAuthenticationMergeKeepsPreferencesAndOtherHostsIndependent(t *testing.T) {
	base := authFixture(
		"alice",
		"dummy-alice",
	) + "other.example:\n  user: other\n  oauth_token: dummy-other\n"
	desired := authFixture(
		"alice",
		"dummy-refreshed-alice",
	) + "other.example:\n  user: other\n  oauth_token: dummy-other\n  git_protocol: ssh\n"
	latest := authFixture(
		"bob",
		"dummy-bob",
	) + "other.example:\n  user: other\n  oauth_token: dummy-other-new\n"
	out, err := mergeHosts([]byte(base), []byte(desired), []byte(latest))
	require.ErrorIs(t, err, errAuthConflict)
	n, err := decode(out)
	require.NoError(t, err)
	require.Equal(t, "bob", get(get(n, testHost), "user").Value)
	require.True(
		t,
		get(get(n, testHost), "oauth_token").Value == "dummy-bob",
		"wrong active credential",
	)
	require.True(
		t,
		get(get(n, "other.example"), "oauth_token").Value == "dummy-other-new",
		"independent identity changed",
	)
	require.Equal(t, "ssh", get(get(n, "other.example"), "git_protocol").Value)
}
