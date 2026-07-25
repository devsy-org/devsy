package tunnelserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/gitcredentials"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gitCredentialsRequest(t *testing.T, host string) *tunnel.Message {
	t.Helper()
	// #nosec G117 -- test request struct carries no secret.
	raw, err := json.Marshal(gitcredentials.GitCredentials{Protocol: "https", Host: host})
	require.NoError(t, err)
	return &tunnel.Message{Message: string(raw)}
}

func TestGitCredentials_TokenReturnedForMatchingHost(t *testing.T) {
	s := New(
		WithAllowGitCredentials(true),
		WithGitToken(&provider.GitToken{
			Host:     "github.com",
			Username: "x-access-token",
			Token:    "ghp_secret",
		}),
	)

	resp, err := s.GitCredentials(context.Background(), gitCredentialsRequest(t, "github.com"))
	require.NoError(t, err)

	var got gitcredentials.GitCredentials
	require.NoError(t, json.Unmarshal([]byte(resp.Message), &got))
	assert.Equal(t, "x-access-token", got.Username)
	assert.Equal(t, "ghp_secret", got.Password)
}

func TestGitCredentials_TokenNotLeakedToOtherHost(t *testing.T) {
	// A mismatched host must NOT receive the token; with no workspace and no
	// platform options it falls through to normal resolution, which must not
	// return our token.
	s := New(
		WithAllowGitCredentials(true),
		WithGitToken(&provider.GitToken{
			Host:     "github.com",
			Username: "x-access-token",
			Token:    "ghp_secret",
		}),
	)

	resp, err := s.GitCredentials(
		context.Background(),
		gitCredentialsRequest(t, "evil.example.com"),
	)
	if err == nil {
		var got gitcredentials.GitCredentials
		require.NoError(t, json.Unmarshal([]byte(resp.Message), &got))
		assert.NotEqual(t, "ghp_secret", got.Password, "token must not be sent to a different host")
	}
}
