package delivery

import (
	"context"
	"io"
	"testing"

	dockerpkg "github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testKubernetesInstallPath = "/home/vscode/.local/bin/devsy"

func TestNewAgentDelivery_LocalDocker(t *testing.T) {
	opts := FactoryOptions{
		WorkspaceConfig: &provider.AgentWorkspaceInfo{
			Agent: provider.ProviderAgentConfig{
				Driver: provider.DockerDriver,
			},
		},
		DockerCommand: defaultDockerCmd,
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

func TestNewAgentDelivery_KubernetesDriver_Native(t *testing.T) {
	podExec := func(_ context.Context, _ []string, _ driver.Streams) error {
		return nil
	}

	opts := FactoryOptions{
		WorkspaceConfig: &provider.AgentWorkspaceInfo{
			Agent: provider.ProviderAgentConfig{
				Driver: provider.KubernetesDriver,
			},
		},
		PodExec: podExec,
	}

	d := NewAgentDelivery(opts)
	native, ok := d.(*KubernetesDelivery)
	require.True(t, ok)
	assert.NotNil(t, native.Exec)
	assert.Equal(t, PhasePostStart, d.Phase())
}

func TestNewAgentDelivery_KubernetesDriver_ThreadsInstallPath(t *testing.T) {
	podExec := func(_ context.Context, _ []string, _ driver.Streams) error {
		return nil
	}

	opts := FactoryOptions{
		WorkspaceConfig: &provider.AgentWorkspaceInfo{
			Agent: provider.ProviderAgentConfig{
				Driver: provider.KubernetesDriver,
			},
		},
		PodExec:                    podExec,
		KubernetesAgentInstallPath: testKubernetesInstallPath,
	}

	d := NewAgentDelivery(opts)
	native, ok := d.(*KubernetesDelivery)
	require.True(t, ok)
	assert.Equal(t, testKubernetesInstallPath, native.InstallPath)
}

func TestNewAgentDelivery_MicrosandboxUsesStreamDelivery(t *testing.T) {
	podExec := func(_ context.Context, _ []string, _ driver.Streams) error {
		return nil
	}

	opts := FactoryOptions{
		WorkspaceConfig: &provider.AgentWorkspaceInfo{
			Agent: provider.ProviderAgentConfig{
				Driver: provider.MicrosandboxDriver,
			},
		},
		PodExec: podExec,
	}

	d := NewAgentDelivery(opts)
	native, ok := d.(*KubernetesDelivery)
	require.True(t, ok)
	assert.NotNil(t, native.Exec)
}

func TestNewAgentDelivery_KubernetesDriver_FallsBackWhenNoPodExec(t *testing.T) {
	execFn := func(ctx context.Context, cmd string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
		return nil
	}

	opts := FactoryOptions{
		WorkspaceConfig: &provider.AgentWorkspaceInfo{
			Agent: provider.ProviderAgentConfig{
				Driver: provider.KubernetesDriver,
			},
		},
		ExecFunc:                   execFn,
		DownloadURL:                "https://artifacts.example.test/devsy",
		KubernetesAgentInstallPath: testKubernetesInstallPath,
		// PodExec intentionally nil → legacy fallback.
	}

	d := NewAgentDelivery(opts)
	legacy, ok := d.(*LegacyShellDelivery)
	require.True(t, ok)
	assert.NotNil(t, legacy.ExecFunc)
	assert.Equal(t, testKubernetesInstallPath, legacy.RemoteAgentPath)
	assert.Equal(t, "https://artifacts.example.test/devsy", legacy.DownloadURL)
	assert.Equal(t, PhasePostStart, d.Phase())
}

func TestIsLocalDockerHost(t *testing.T) {
	assert.True(t, dockerpkg.IsLocalDockerHost(""))
	assert.True(t, dockerpkg.IsLocalDockerHost("unix:///var/run/docker.sock"))
	assert.True(t, dockerpkg.IsLocalDockerHost("unix:///home/user/.docker/desktop/docker.sock"))
	assert.True(t, dockerpkg.IsLocalDockerHost("npipe:////./pipe/docker_engine"))
	assert.True(t, dockerpkg.IsLocalDockerHost("npipe:////./pipe/podman-machine-default"))
	assert.False(t, dockerpkg.IsLocalDockerHost("tcp://192.168.1.100:2376"))
	assert.False(t, dockerpkg.IsLocalDockerHost("ssh://user@remote-host"))
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

func TestNewAgentDelivery_AppleUsesShellDelivery(t *testing.T) {
	opts := FactoryOptions{
		WorkspaceConfig: &provider.AgentWorkspaceInfo{
			Agent: provider.ProviderAgentConfig{Driver: provider.AppleDriver},
		},
	}
	d := NewAgentDelivery(opts)
	if _, ok := d.(*LegacyShellDelivery); !ok {
		t.Fatalf("apple driver must use shell delivery, got %T", d)
	}
}

func TestNewAgentDelivery_MicrosandboxUsesShellDelivery(t *testing.T) {
	opts := FactoryOptions{
		WorkspaceConfig: &provider.AgentWorkspaceInfo{
			Agent: provider.ProviderAgentConfig{Driver: provider.MicrosandboxDriver},
		},
	}
	d := NewAgentDelivery(opts)
	if _, ok := d.(*LegacyShellDelivery); !ok {
		t.Fatalf("microsandbox driver must use shell delivery, got %T", d)
	}
}
