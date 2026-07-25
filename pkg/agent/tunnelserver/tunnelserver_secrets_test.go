package tunnelserver

import (
	"context"
	"testing"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSecrets_ReturnsEnvAndMountEntries(t *testing.T) {
	s := New(WithSecrets(
		[]string{"API_KEY=abc", "NO_EQUALS"},
		[]string{"tls.key=keydata"},
	))

	resp, err := s.Secrets(context.Background(), &tunnel.Empty{})
	require.NoError(t, err)

	got := map[string]*tunnel.Secret{}
	for _, sec := range resp.GetSecrets() {
		got[sec.GetName()] = sec
	}

	require.Contains(t, got, "API_KEY")
	assert.Equal(t, "abc", got["API_KEY"].GetValue())
	assert.False(t, got["API_KEY"].GetMount())

	require.Contains(t, got, "tls.key")
	assert.Equal(t, "keydata", got["tls.key"].GetValue())
	assert.True(t, got["tls.key"].GetMount())

	assert.NotContains(t, got, "NO_EQUALS")
}

func TestSecrets_EmptyWhenUnset(t *testing.T) {
	s := New()

	resp, err := s.Secrets(context.Background(), &tunnel.Empty{})
	require.NoError(t, err)
	assert.Empty(t, resp.GetSecrets())
}
