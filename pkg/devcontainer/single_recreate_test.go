package devcontainer

import (
	"errors"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/stretchr/testify/require"
)

type secretMigrationDriver struct {
	*migrationMockDriver
	tmpfsSupported bool
}

func (d *secretMigrationDriver) SupportsMountType(mountType string) bool {
	return mountType == driver.MountTypeTmpfs && d.tmpfsSupported
}

func newSecretMigrationRunner() (*runner, *resolveParams, *secretMigrationDriver) {
	d := &secretMigrationDriver{
		migrationMockDriver: &migrationMockDriver{
			provisioningPreflightMockDriver: &provisioningPreflightMockDriver{
				mockDriver: &mockDriver{},
			},
		},
	}
	params := recreateResolveParams()
	params.options.Recreate = false
	return newTestRunner(d), params, d
}

func TestSecretMigrationPreflightFailurePreservesExistingContainer(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		terminal, file, driver   bool
		explicit, tmpfsSupported bool
	}{
		{name: "terminal", terminal: true, tmpfsSupported: true},
		{name: "file", file: true, tmpfsSupported: true},
		{name: "both", terminal: true, file: true, tmpfsSupported: true},
		{name: "driver migration precedes secrets", terminal: true, file: true, driver: true},
		{name: "explicit recreation skips migration", terminal: true, file: true, explicit: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, params, d := newSecretMigrationRunner()
			d.required, d.tmpfsSupported = tc.driver, tc.tmpfsSupported
			d.provisioningErr = errors.New("provisioning unavailable")
			params.options.Recreate = tc.explicit
			if tc.terminal {
				r.workspaceConfig.CLIOptions.TerminalSecretEnvNames = []string{
					terminalSecretSentinelName,
				}
			}
			if tc.file {
				params.options.SecretsMount = []string{secretFileMountRequestSentinel}
			}
			_, err := r.resolveContainer(t.Context(), params, runningContainerDetails())
			require.ErrorIs(t, err, d.provisioningErr)
			require.True(t, params.options.Recreate)
			require.True(t, d.provisioningCalled)
			require.False(t, d.stopCalled)
			require.False(t, d.deleteCalled)
		})
	}
}

func TestSecretMigrationRejectsBeforeProvisioning(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		terminal, file, external bool
		tmpfsSupported           bool
		want                     string
	}{
		{name: "terminal unsupported", terminal: true, want: "securely injecting workspace secrets"},
		{name: "file unsupported", file: true, want: "securely mounting workspace file secrets"},
		{
			name: "terminal external", terminal: true, external: true, tmpfsSupported: true,
			want: "cannot inject attached terminal secrets into externally managed container",
		},
		{
			name: "file external", file: true, external: true, tmpfsSupported: true,
			want: "cannot inject file secrets into externally managed container",
		},
		{
			name: "terminal rejection precedes file", terminal: true, file: true,
			external: true, tmpfsSupported: true, want: "cannot inject attached terminal secrets",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, params, d := newSecretMigrationRunner()
			d.tmpfsSupported = tc.tmpfsSupported
			if tc.terminal {
				r.workspaceConfig.CLIOptions.TerminalSecretEnvNames = []string{
					terminalSecretSentinelName,
				}
			}
			if tc.file {
				params.options.SecretsMount = []string{secretFileMountRequestSentinel}
			}
			if tc.external {
				params.parsedConfig.Config.ContainerID = testContainerID
			}
			_, err := r.resolveContainer(t.Context(), params, runningContainerDetails())
			require.ErrorContains(t, err, tc.want)
			require.False(t, params.options.Recreate)
			require.False(t, d.provisioningCalled)
			require.False(t, d.stopCalled)
			require.False(t, d.deleteCalled)
		})
	}
}

func TestPrepareContainerRecreationKeepsCompatibleContainer(t *testing.T) {
	for _, details := range []*config.ContainerDetails{
		nil,
		{Mounts: []config.ContainerMount{
			{Type: driver.MountTypeTmpfs, Destination: config.SecretsEnvDir},
			{Type: driver.MountTypeTmpfs, Destination: config.SecretsMountDir},
		}},
	} {
		r, params, d := newSecretMigrationRunner()
		r.workspaceConfig.CLIOptions.TerminalSecretEnvNames = []string{terminalSecretSentinelName}
		params.options.SecretsMount = []string{secretFileMountRequestSentinel}
		require.NoError(t, r.prepareContainerRecreation(t.Context(), details, params))
		require.False(t, params.options.Recreate)
		require.False(t, d.provisioningCalled)
	}
}
