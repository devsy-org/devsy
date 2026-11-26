package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGitHubForwardingDefaultsOffAndIsIndependent(t *testing.T) {
	for _, value := range []string{"", BoolFalse, BoolTrue} {
		c := &Config{
			Contexts: map[string]*ContextConfig{
				DefaultContext: {
					Options: map[string]OptionValue{
						ContextOptionGitHubCLIForwarding: {Value: value},
					},
				},
			},
			DefaultContext: DefaultContext,
		}
		want := BoolFalse
		if value == BoolTrue {
			want = BoolTrue
		}
		require.Equal(t, want, c.ContextOption(ContextOptionGitHubCLIForwarding))
		require.Equal(t, BoolTrue, c.ContextOption(ContextOptionSSHInjectGitCredentials))
		require.Equal(t, BoolTrue, c.ContextOption(ContextOptionSSHInjectDockerCredentials))
	}
}
