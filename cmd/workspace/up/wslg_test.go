package up

import (
	"context"
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

func TestWSLgPreflightRunsFirstInPostResolveWorkspace(t *testing.T) {
	cfg := testConfig()
	cfg.Current().Options = map[string]config.OptionValue{
		config.ContextOptionWSLgEnabled: {Value: config.BoolTrue},
	}
	// The fake has no workspace config, so reaching secrets or provider update
	// would fail differently; only an early preflight yields ErrUnsupported.
	for _, existed := range []bool{false, true} {
		client := &fakeWorkspaceClient{}
		err := (&UpCmd{}).postResolveWorkspace(context.Background(), cfg, client, existed)
		require.ErrorIs(t, err, wslg.ErrUnsupported)
		require.Equal(t, !existed, client.deleted, "only a newly created workspace is removed")
	}
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
