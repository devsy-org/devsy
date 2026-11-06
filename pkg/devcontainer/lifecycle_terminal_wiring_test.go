package devcontainer

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type wiringClient struct{ tunnel.TunnelClient }

type LifecycleTerminalWiringSuite struct {
	suite.Suite
}

func TestLifecycleTerminalWiringSuite(t *testing.T) {
	suite.Run(t, new(LifecycleTerminalWiringSuite))
}

func (s *LifecycleTerminalWiringSuite) TestUpOptionsTerminalClientNotSerialized() {
	t := s.T()
	data, err := json.Marshal(UpOptions{LifecycleTerminalClient: &wiringClient{}})
	require.NoError(t, err)
	require.NotContains(t, string(data), "LifecycleTerminalClient")
}

func (s *LifecycleTerminalWiringSuite) TestUpClearsTerminalClientOnError() {
	t := s.T()
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
