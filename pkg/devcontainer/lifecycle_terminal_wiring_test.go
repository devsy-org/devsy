package devcontainer

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
)

type wiringClient struct{ tunnel.TunnelClient }

func TestUpOptionsTerminalClientNotSerialized(t *testing.T) {
	data, err := json.Marshal(UpOptions{LifecycleTerminalClient: &wiringClient{}})
	require.NoError(t, err)
	require.NotContains(t, string(data), "LifecycleTerminalClient")
}

func TestUpClearsTerminalClientOnError(t *testing.T) {
	t.Setenv("DEVSY_CONFIG", t.TempDir())
	r := &runner{workspaceConfig: &provider.AgentWorkspaceInfo{Workspace: &provider.Workspace{}}}
	_, err := r.Up(
		context.Background(),
		UpOptions{LifecycleTerminalClient: &wiringClient{}},
		0,
		nil,
	)
	require.Error(t, err)
	require.Nil(t, r.lifecycleTerminalClient)
}
