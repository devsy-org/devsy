package devcontainer

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
)

const (
	terminalSecretSentinelName     = "SENTINEL_NAME"
	secretFileMountRequestSentinel = "FILE_SECRET=sentinel"
)

type terminalSecretMountDriver struct {
	driver.Driver
	supported bool
}

func (d terminalSecretMountDriver) SupportsMountType(string) bool {
	return d.supported
}

func TestHasTerminalSecretEnvironmentMount(t *testing.T) {
	tests := []struct {
		name    string
		details *config.ContainerDetails
		want    bool
	}{
		{name: "nil container"},
		{name: "no mounts", details: &config.ContainerDetails{}},
		{
			name: "unrelated tmpfs",
			details: &config.ContainerDetails{Mounts: []config.ContainerMount{{
				Type: driver.MountTypeTmpfs, Destination: "/tmp",
			}}},
		},
		{
			name: "bind at secret path",
			details: &config.ContainerDetails{Mounts: []config.ContainerMount{{
				Type: driver.MountTypeBind, Destination: config.SecretsEnvDir,
			}}},
		},
		{
			name: "tmpfs at secret path",
			details: &config.ContainerDetails{Mounts: []config.ContainerMount{{
				Type: driver.MountTypeTmpfs, Destination: config.SecretsEnvDir,
			}}},
			want: true,
		},
		{
			name: "normalizes destination",
			details: &config.ContainerDetails{Mounts: []config.ContainerMount{{
				Type: driver.MountTypeTmpfs, Destination: config.SecretsEnvDir + "/",
			}}},
			want: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, hasTerminalSecretEnvironmentMount(test.details))
		})
	}
}

func TestTerminalSecretEnvironmentMigrationRequiresTmpfsSupport(t *testing.T) {
	workspace := &provider.AgentWorkspaceInfo{}
	workspace.CLIOptions.TerminalSecretEnvNames = []string{terminalSecretSentinelName}
	details := &config.ContainerDetails{}
	r := &runner{workspaceConfig: workspace, driver: terminalSecretMountDriver{}}
	params := &resolveParams{}
	params.options.Recreate = false

	require.True(t, r.needsTerminalSecretEnvironmentMigration(details))
	require.Error(t, r.validateTerminalSecretEnvironmentSupport())
	require.False(t, params.options.Recreate)

	r.driver = terminalSecretMountDriver{supported: true}
	require.NoError(t, r.validateTerminalSecretEnvironmentSupport())
}

func TestNeedsSecretFileMountMigration(t *testing.T) {
	details := &config.ContainerDetails{}
	require.True(
		t,
		needsSecretFileMountMigration(details, []string{secretFileMountRequestSentinel}),
	)
	require.False(t, needsSecretFileMountMigration(details, nil))

	details.Mounts = []config.ContainerMount{{
		Type: driver.MountTypeTmpfs, Destination: config.SecretsMountDir,
	}}
	require.False(
		t,
		needsSecretFileMountMigration(details, []string{secretFileMountRequestSentinel}),
	)

	details.Mounts[0].Type = mountTypeBind
	require.True(
		t,
		needsSecretFileMountMigration(details, []string{secretFileMountRequestSentinel}),
	)
}

func TestResolveContainerFailsClosedForSecretMountMigration(t *testing.T) {
	workspace := &provider.AgentWorkspaceInfo{}
	workspace.CLIOptions.TerminalSecretEnvNames = []string{terminalSecretSentinelName}
	details := &config.ContainerDetails{}
	parsed := &config.SubstitutedConfig{Config: &config.DevContainerConfig{}}

	r := &runner{workspaceConfig: workspace, driver: terminalSecretMountDriver{}}
	_, err := r.resolveContainer(t.Context(), &resolveParams{parsedConfig: parsed}, details)
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not support securely injecting workspace secrets")

	r.driver = terminalSecretMountDriver{supported: true}
	parsed.Config.ContainerID = "external-container"
	_, err = r.resolveContainer(t.Context(), &resolveParams{parsedConfig: parsed}, details)
	require.Error(t, err)
	require.Contains(t, err.Error(), "externally managed container")
}
