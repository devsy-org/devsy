package devcontainer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	composetypes "github.com/compose-spec/compose-go/v2/types"
	"github.com/devsy-org/devsy/pkg/compose"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
)

func TestStartComposeContainerRecreateRefreshesOverrides(t *testing.T) {
	for _, reset := range []bool{false, true} {
		name := "recreate"
		if reset {
			name = "reset_recreate"
		}
		t.Run(name, func(t *testing.T) {
			staleOverride := filepath.Join(
				t.TempDir(),
				FeaturesStartOverrideFilePrefix+"-stale.yml",
			)
			require.NoError(t, os.WriteFile(staleOverride, []byte("stale"), 0o600))
			container := &config.ContainerDetails{
				ID:    testContainerID,
				State: config.ContainerDetailsState{Status: config.ContainerStatusRunning},
				Config: config.ContainerDetailsConfig{
					Labels: map[string]string{ConfigFilesLabel: staleOverride},
				},
			}
			buildErr := errors.New("fresh image inspection failed")
			d := &mockDriver{stopErr: errors.New("stale overrides reached teardown")}
			r := newTestRunner(d)
			r.imageBackend = &separateImages{err: buildErr}
			_, err := r.startContainer(context.Background(), &startContainerParams{
				parsedConfig: &config.SubstitutedConfig{
					Config: &config.DevContainerConfig{
						ComposeContainer: config.ComposeContainer{
							Service: composeSecretTestServiceName,
						},
					},
				},
				substitutionContext: &config.SubstitutionContext{},
				project: &composetypes.Project{
					Name: "recreate-test",
					Services: composetypes.Services{
						composeSecretTestServiceName: {
							Name:  composeSecretTestServiceName,
							Image: "test:latest",
						},
					},
				},
				composeHelper: &compose.ComposeHelper{},
				container:     container,
				options: UpOptions{
					CLIOptions: provider.CLIOptions{Recreate: true, Reset: reset},
				},
			})
			require.ErrorIs(t, err, buildErr)
			require.False(t, d.stopCalled)
			require.False(t, d.deleteCalled)
		})
	}
}
