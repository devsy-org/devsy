package delivery

import (
	"context"
	"io"
	"testing"

	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAgentDelivery_LocalDocker(t *testing.T) {
	opts := FactoryOptions{
		WorkspaceConfig: &provider.AgentWorkspaceInfo{
			Agent: provider.ProviderAgentConfig{
				Driver: provider.DockerDriver,
			},
		},
		DockerCommand: "docker",
	}

	d := NewAgentDelivery(opts)
	assert.IsType(t, &LocalDockerDelivery{}, d)
	assert.Equal(t, PhasePreStart, d.Phase())
}

func TestNewAgentDelivery_EmptyDriver_DefaultsToLocal(t *testing.T) {
	opts := FactoryOptions{
		WorkspaceConfig: &provider.AgentWorkspaceInfo{
			Agent: provider.ProviderAgentConfig{
				Driver: "",
			},
		},
	}

	d := NewAgentDelivery(opts)
	assert.IsType(t, &LocalDockerDelivery{}, d)
}

func TestNewAgentDelivery_RemoteDocker(t *testing.T) {
	opts := FactoryOptions{
		WorkspaceConfig: &provider.AgentWorkspaceInfo{
			Agent: provider.ProviderAgentConfig{
				Driver: provider.DockerDriver,
			},
		},
		IsRemoteDocker: true,
		ContainerID:    "abc123",
	}

	d := NewAgentDelivery(opts)
	assert.IsType(t, &RemoteDockerDelivery{}, d)
	assert.Equal(t, PhasePostStart, d.Phase())
}

func TestNewAgentDelivery_CustomDriver(t *testing.T) {
	execFn := func(ctx context.Context, cmd string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
		return nil
	}

	opts := FactoryOptions{
		WorkspaceConfig: &provider.AgentWorkspaceInfo{
			Agent: provider.ProviderAgentConfig{
				Driver: provider.CustomDriver,
			},
		},
		ExecFunc: execFn,
	}

	d := NewAgentDelivery(opts)
	assert.IsType(t, &LegacyShellDelivery{}, d)
	assert.Equal(t, PhasePostStart, d.Phase())
}

func TestNewAgentDelivery_KubernetesDriver_FallsToLegacy(t *testing.T) {
	opts := FactoryOptions{
		WorkspaceConfig: &provider.AgentWorkspaceInfo{
			Agent: provider.ProviderAgentConfig{
				Driver: provider.KubernetesDriver,
			},
		},
	}

	d := NewAgentDelivery(opts)
	assert.IsType(t, &LegacyShellDelivery{}, d)
}

func TestIsDockerLocal(t *testing.T) {
	assert.True(t, isLocalDockerHost(""))
	assert.True(t, isLocalDockerHost("unix:///var/run/docker.sock"))
	assert.True(t, isLocalDockerHost("unix:///home/user/.docker/desktop/docker.sock"))
	assert.True(t, isLocalDockerHost("npipe:////./pipe/docker_engine"))
	assert.False(t, isLocalDockerHost("tcp://192.168.1.100:2376"))
	assert.False(t, isLocalDockerHost("ssh://user@remote-host"))
}

func TestDeliver_PreStart(t *testing.T) {
	d := &LegacyShellDelivery{}
	err := Deliver(context.Background(), d, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "post-start options required")
}

func TestDeliver_UnknownPhase(t *testing.T) {
	d := &mockDelivery{phase: DeliveryPhase(99)}
	err := Deliver(context.Background(), d, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown delivery phase")
}

type mockDelivery struct {
	phase DeliveryPhase
}

func (m *mockDelivery) Phase() DeliveryPhase { return m.phase }

func (m *mockDelivery) DeliverPreStart(_ context.Context, _ PreStartOptions) error {
	return nil
}

func (m *mockDelivery) DeliverPostStart(_ context.Context, _ PostStartOptions) error {
	return nil
}

func (m *mockDelivery) Cleanup(_ context.Context, _ string) error { return nil }
