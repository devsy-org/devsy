package devcontainer

import (
	"context"
	"errors"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/metadata"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/stretchr/testify/require"
)

const reuseCurrentUser = "requested-user"

type reusePreflightMockDriver struct {
	*provisioningPreflightMockDriver
	err                     error
	workspaceID, remoteUser string
	calls                   int
}

func (d *reusePreflightMockDriver) ReusePreflight(_ context.Context, id, user string) error {
	d.calls++
	d.workspaceID, d.remoteUser = id, user
	return d.err
}

func (*reusePreflightMockDriver) RecreateMode() driver.RecreateMode { return driver.RecreateDelete }

func TestReusePreflightFailurePreservesExistingWorkspace(t *testing.T) {
	sentinel := errors.New("ownership changed; rerun with --recreate")
	d := &reusePreflightMockDriver{
		provisioningPreflightMockDriver: &provisioningPreflightMockDriver{
			mockDriver: &mockDriver{},
		},
		err: sentinel,
	}
	p := recreateResolveParams()
	p.options.Recreate = false
	p.parsedConfig.Config.RemoteUser = reuseCurrentUser
	p.substitutionContext = &config.SubstitutionContext{}
	details := runningContainerDetails()
	details.Config.Labels[metadata.CreationConfigLabel] = stringTrue
	details.Config.Labels[metadata.ImageMetadataLabel] = `[{"remoteUser":"old-user"}]`
	r := newTestRunner(d)
	_, err := r.resolveContainer(context.Background(), p, details)
	require.ErrorIs(t, err, sentinel)
	require.Equal(t, r.id, d.workspaceID)
	require.Equal(t, reuseCurrentUser, d.remoteUser)
	require.False(t, p.options.Recreate)
	require.False(t, d.provisioningCalled)
	require.False(t, d.stopCalled)
	require.False(t, d.deleteCalled)
	require.Equal(t, testStatusRunning, string(details.State.Status))
}

func TestReusePreflightSkippedForCreation(t *testing.T) {
	d := &reusePreflightMockDriver{
		provisioningPreflightMockDriver: &provisioningPreflightMockDriver{
			mockDriver: &mockDriver{},
		},
		err: errors.New("must not validate reuse"),
	}
	r := newTestRunner(d)
	p := recreateResolveParams()
	require.NoError(
		t,
		r.applyDriverRecreateRequirement(context.Background(), runningContainerDetails(), p),
	)
	p.options.Recreate = false
	require.NoError(t, r.applyDriverRecreateRequirement(context.Background(), nil, p))
	require.Zero(t, d.calls)
}

func TestReusePreflightRefreshesDeveloperIdentity(t *testing.T) {
	const featureUser = "feature-user"
	for _, tc := range []struct{ name, label, value, want string }{
		{"creation marker", metadata.CreationConfigLabel, stringTrue, featureUser},
		{"legacy managed workspace", overlayStructureLabel, structuralSignature(&config.DevContainerConfig{}), featureUser},
		{"unmarked image metadata", "", "", "old-user"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &reusePreflightMockDriver{
				provisioningPreflightMockDriver: &provisioningPreflightMockDriver{
					mockDriver: &mockDriver{},
				},
			}
			r := newTestRunner(d)
			p := recreateResolveParams()
			p.options.Recreate = false
			p.parsedConfig.Config.RemoteUser = ""
			p.substitutionContext = &config.SubstitutionContext{}
			details := runningContainerDetails()
			if tc.label != "" {
				details.Config.Labels[tc.label] = tc.value
			}
			details.Config.Labels[metadata.ImageMetadataLabel] = `[{"remoteUser":"feature-user"},{"remoteUser":"old-user"}]`
			require.NoError(t, r.applyDriverRecreateRequirement(context.Background(), details, p))
			require.Equal(t, tc.want, d.remoteUser)
			merged, err := r.mergeExistingContainerConfig(context.Background(), details, p)
			require.NoError(t, err)
			require.Equal(t, tc.want, merged.RemoteUser)
			p.parsedConfig.Config.RemoteUser = reuseCurrentUser
			merged, err = r.mergeExistingContainerConfig(context.Background(), details, p)
			require.NoError(t, err)
			require.Equal(t, reuseCurrentUser, merged.RemoteUser)
		})
	}
}
