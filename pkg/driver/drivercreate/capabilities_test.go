package drivercreate

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/driver/kubernetes"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuiltInCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name                                        string
		runtime                                     string
		images, imageRunner, streaming, chown, argv bool
		recreate                                    driver.RecreateMode
	}{
		{name: provider.DockerDriver, images: true, imageRunner: true, recreate: driver.RecreateDelete},
		{name: provider.DockerDriver, runtime: "podman", images: true, imageRunner: true, recreate: driver.RecreateDelete},
		{name: provider.AppleDriver, images: true, imageRunner: true, recreate: driver.RecreateDelete},
		{name: provider.CustomDriver, streaming: true, chown: true, recreate: driver.RecreateStop},
		{
			name: provider.MicrosandboxDriver, images: true, imageRunner: true,
			chown: true, argv: true, recreate: driver.RecreateDelete,
		},
	} {
		t.Run(tc.name+tc.runtime, func(t *testing.T) {
			if tc.name == provider.AppleDriver &&
				(runtime.GOOS != "darwin" || runtime.GOARCH != "arm64") {
				t.Skip("Apple requires macOS arm64")
			}
			info := &provider.AgentWorkspaceInfo{
				Workspace: &provider.Workspace{},
				Agent: provider.ProviderAgentConfig{
					Driver: tc.name,
					Docker: provider.ProviderDockerDriverConfig{
						Runtime:   tc.runtime,
						Elevation: "none",
					},
				},
			}
			bundle, err := New(context.Background(), info)
			require.NoError(t, err)
			assert.Equal(t, tc.images, bundle.Images != nil)
			_, imageRunner := bundle.Runtime.(driver.ImageRunner)
			_, argv := bundle.Runtime.(driver.ArgvExecDriver)
			assert.Equal(t, tc.imageRunner, imageRunner)
			assert.Equal(t, tc.argv, argv)
			assert.Equal(t, tc.streaming, driver.DriverRequiresMountStreaming(bundle.Runtime))
			assert.Equal(t, tc.chown, driver.DriverRequiresWorkspaceChown(bundle.Runtime))
			assert.Equal(t, tc.recreate, driver.DriverRecreateMode(bundle.Runtime))
			if tc.name == provider.MicrosandboxDriver {
				assert.NotSame(t, bundle.Runtime, bundle.Images)
				_, runtimeImages := bundle.Runtime.(driver.ImageBackend)
				assert.False(t, runtimeImages)
				assert.Nil(t, bundle.Images.(*microsandboxImages).backend)
			} else if tc.images {
				assert.Same(t, bundle.Runtime, bundle.Images)
			}
		})
	}
}

func TestKubernetesBundle(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config")
	require.NoError(t, os.WriteFile(cfg, []byte(`apiVersion: v1
kind: Config
clusters:
- name: test
  cluster:
    server: https://127.0.0.1:6443
contexts:
- name: test
  context:
    cluster: test
    user: test
current-context: test
users:
- name: test
  user:
    token: test
`), 0o600))
	bundle, err := New(
		context.Background(),
		&provider.AgentWorkspaceInfo{Agent: provider.ProviderAgentConfig{
			Driver:     provider.KubernetesDriver,
			Kubernetes: provider.ProviderKubernetesDriverConfig{KubernetesConfig: cfg},
		}},
	)
	require.NoError(t, err)
	assert.IsType(t, &kubernetes.KubernetesDriver{}, bundle.Runtime)
	assert.Nil(t, bundle.Images)
	assert.True(t, driver.DriverRequiresMountStreaming(bundle.Runtime))
	assert.True(t, driver.DriverRequiresWorkspaceChown(bundle.Runtime))
	assert.Equal(t, driver.RecreateStop, driver.DriverRecreateMode(bundle.Runtime))
	_, argv := bundle.Runtime.(driver.ArgvExecDriver)
	assert.True(t, argv)
	_, imageRunner := bundle.Runtime.(driver.ImageRunner)
	assert.False(t, imageRunner)
}

func TestMicrosandboxDoesNotInitializeDockerForRegistryTags(t *testing.T) {
	bundle, err := New(
		context.Background(),
		&provider.AgentWorkspaceInfo{Agent: provider.ProviderAgentConfig{
			Driver: provider.MicrosandboxDriver,
			Docker: provider.ProviderDockerDriverConfig{Builder: "invalid-builder"},
		}},
	)
	require.NoError(t, err)
	tag, err := bundle.Images.GetImageTag(context.Background(), "alpine:latest")
	require.NoError(t, err)
	assert.Equal(t, "alpine:latest", tag)
	err = bundle.Images.PushDevContainer(context.Background(), tag)
	require.ErrorContains(t, err, "microsandbox needs docker")
}
