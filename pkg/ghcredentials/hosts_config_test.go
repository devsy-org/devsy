package ghcredentials

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHostsRejectMalformedAuthenticationAtEveryMergeBoundary(t *testing.T) {
	for _, malformed := range []string{
		"github.com: [a, b]\n",
		"github.com:\n  users: [a, b]\n",
		"github.com:\n  users:\n    alice: [a, b]\n",
		"github.com:\n  user: [alice, bob]\n",
		"github.com:\n  oauth_token: {secret: value}\n",
		"github.com:\n  users:\n    alice:\n      oauth_token: [a, b]\n",
		"github.com:\n  users: {}\n  users: [a, b]\n",
		"github.com:\n  users:\n    alice: {}\n    alice: [a, b]\n",
	} {
		for index := range 3 {
			inputs := [3][]byte{
				[]byte(authFixture("alice", "dummy-a")),
				[]byte(authFixture("alice", "dummy-b")),
				[]byte(authFixture("alice", "dummy-a")),
			}
			inputs[index] = []byte(malformed)
			_, err := mergeHosts(inputs[0], inputs[1], inputs[2])
			require.ErrorIs(t, err, errInvalidConfig)
		}
	}
}

func TestHostsAcceptNullRegistrationsAndArbitraryPreferences(t *testing.T) {
	data := []byte(`github.com:
  user: alice
  oauth_token:
  users:
    alice:
    bob: {}
    dave: ""
    carol:
      oauth_token: ""
      custom: [one, two]
  custom: [one, two]
`)
	_, err := decodeHosts(data)
	require.NoError(t, err)
	_, err = mergeHosts(nil, data, nil)
	require.NoError(t, err)
	_, err = mergeYAML(nil, []byte("custom: [one, two]\n"), nil)
	require.NoError(t, err)
}

func TestHostsPreserveScalarUsernamesAcceptedByRealCLI(t *testing.T) {
	for _, user := range []string{"12345", testScalarBooleanUser, "1.5"} {
		data := []byte("github.com:\n  user: " + user + "\n  users:\n    " + user + ": {}\n")
		n, err := decodeHosts(data)
		require.NoError(t, err)
		require.True(t, registeredAccount(get(n, "github.com")))
		merged, err := mergeHosts(data, data, data)
		require.NoError(t, err)
		require.Equal(t, data, merged)
	}
}

const testScalarBooleanUser = "true"
