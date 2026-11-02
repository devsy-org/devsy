package up

import (
	"runtime"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/wslg"
	"github.com/stretchr/testify/require"
)

func TestWSLgPreflightBeforeWorkspaceProvisioning(t *testing.T) {
	cfg := testConfig()
	cmd := &UpCmd{}
	require.NoError(t, cmd.preflightWSLgContext(cfg))
	cfg.Current().Options = map[string]config.OptionValue{
		config.ContextOptionWSLgEnabled: {Value: config.BoolTrue},
	}
	if runtime.GOOS == wslgGoosWindows {
		require.ErrorIs(t, cmd.preflightWSLgContext(cfg), wslg.ErrInvalidTarget)
	} else {
		require.ErrorIs(t, cmd.preflightWSLgContext(cfg), wslg.ErrUnsupported)
	}
	require.ErrorIs(t, cmd.preflightWSLgClient(cfg, &fakeWorkspaceClient{}), wslg.ErrUnsupported)
}

func TestWSLgResolvedAgentRejectsUnsupportedDriversAndRuntimes(t *testing.T) {
	for _, agent := range []provider.ProviderAgentConfig{
		{Driver: provider.ExternalDriver},
		{Driver: provider.CustomDriver},
		{Driver: provider.DockerDriver, Docker: provider.ProviderDockerDriverConfig{Runtime: "podman"}},
		{Driver: provider.DockerDriver, Docker: provider.ProviderDockerDriverConfig{Runtime: "nerdctl"}},
	} {
		require.ErrorIs(t, validateWSLgAgent(agent), wslg.ErrUnsupported)
	}
	require.NoError(
		t,
		validateWSLgAgent(
			provider.ProviderAgentConfig{
				Driver: provider.DockerDriver,
				Docker: provider.ProviderDockerDriverConfig{Runtime: "docker"},
			},
		),
	)
}
