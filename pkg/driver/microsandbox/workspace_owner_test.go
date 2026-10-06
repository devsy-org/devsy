package microsandbox

import (
	"context"
	"errors"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/stretchr/testify/require"
)

const testNumericIdentity = "1000:1001"

type recordingUserResolver struct {
	image string
	built bool
	user  string
	owner *mountOwner
	err   error
	calls int
}

func (r *recordingUserResolver) Resolve(
	_ context.Context,
	image string,
	built bool,
	user string,
) (*mountOwner, error) {
	r.image, r.built, r.user = image, built, user
	r.calls++
	return r.owner, r.err
}

func TestWorkspaceOwnerKeepsContainerIdentity(t *testing.T) {
	f := newFakeClient()
	d := newDriver(f, nil, specDefaults{})
	resolver := &recordingUserResolver{owner: &mountOwner{UID: 1000, GID: 1001}}
	d.userResolver = resolver
	options := &driver.RunOptions{
		Image:      testImage,
		ImageBuilt: true,
		User:       rootUser,
		RemoteUser: testUser,
		WorkspaceMount: &config.Mount{
			Type:   driver.MountTypeBind,
			Source: testBindSrc,
			Target: testBindDst,
		},
		Mounts: []*config.Mount{
			{Type: driver.MountTypeBind, Source: "/extra", Target: "/extra"},
		},
	}
	require.NoError(
		t,
		d.RunImageDevContainer(
			context.Background(),
			&driver.RunImageDevContainerParams{WorkspaceID: wsID, Options: options},
		),
	)
	require.Equal(t, testUser, resolver.user)
	require.Equal(t, testImage, resolver.image)
	require.True(t, resolver.built)
	spec := f.created[wsName]
	require.Equal(t, rootUser, spec.User)
	require.Equal(
		t,
		testBindSrc+":"+testBindDst+":stat-virt=strict,host-perms=mirror,uid=1000,gid=1001",
		bindMountSpec(spec.Mounts[0]),
	)
	require.Nil(t, spec.Mounts[1].Policy.Owner)
	require.Equal(t, d.workspaceMountContract(), spec.Labels[workspaceMountContractLabel])
}

func TestWorkspaceOwnerFailurePreservesExistingSandbox(t *testing.T) {
	f := newFakeClient()
	f.info[wsName] = &sandboxInfo{Name: wsName, Running: true}
	d := newDriver(f, nil, specDefaults{})
	d.userResolver = &recordingUserResolver{err: errors.New("user not found")}
	err := d.RunDevContainer(
		context.Background(),
		wsID,
		&driver.RunOptions{
			Image:          imgX,
			RemoteUser:     "missing",
			WorkspaceMount: &config.Mount{Source: testBindSrc, Target: testBindDst},
		},
	)
	require.ErrorContains(t, err, `remoteUser "missing"`)
	require.Empty(t, f.calls)
	require.True(t, f.info[wsName].Running)
}

func TestWorkspaceOwnerPolicyOff(t *testing.T) {
	d := newDriver(newFakeClient(), nil, specDefaults{})
	d.workspaceMountPolicy = workspaceMountPolicy{
		StatVirtualization: statVirtOff,
		HostPermissions:    hostPermissionsPrivate,
	}
	resolver := &recordingUserResolver{err: errors.New("must not resolve")}
	d.userResolver = resolver
	options := &driver.RunOptions{
		WorkspaceMount: &config.Mount{Source: testBindSrc, Target: testBindDst},
		RemoteUser:     testUser,
		Dockerless:     true,
	}
	owner, err := d.resolveWorkspaceOwner(context.Background(), options)
	require.NoError(t, err)
	require.Nil(t, owner)
	require.Zero(t, resolver.calls)
	mount := d.workspaceMount(options.WorkspaceMount, &mountOwner{UID: 1000, GID: 1000})
	require.Equal(
		t,
		testBindSrc+":"+testBindDst+":stat-virt=off,host-perms=private",
		bindMountSpec(*mount),
	)
}

func TestWorkspaceMountContractRecreation(t *testing.T) {
	d := newDriver(newFakeClient(), nil, specDefaults{})
	for _, label := range []string{"", "v1", d.workspaceMountContract(), "v2;stat=relaxed;host=mirror;owner=remote-user"} {
		details := &config.ContainerDetails{
			Config: config.ContainerDetailsConfig{
				Labels: map[string]string{
					workspaceMountContractLabel: label,
					workspaceRemoteUserLabel:    rootUser,
				},
			},
		}
		required, _ := driver.DriverRequiresRecreate(d, details, rootUser)
		require.Equal(t, label != d.workspaceMountContract(), required)
	}
	required, _ := d.RequiresRecreate(nil, rootUser)
	require.False(t, required)
}

func TestWorkspaceOwnerFallbackIdentity(t *testing.T) {
	for _, tt := range []struct{ user, remote, want string }{
		{"node", "", "node"}, {"", "", rootUser}, {rootUser, testUser, testUser},
	} {
		d := newDriver(newFakeClient(), nil, specDefaults{})
		resolver := &recordingUserResolver{owner: &mountOwner{}}
		d.userResolver = resolver
		_, err := d.resolveWorkspaceOwner(
			context.Background(),
			&driver.RunOptions{
				User:           tt.user,
				RemoteUser:     tt.remote,
				WorkspaceMount: &config.Mount{Source: testBindSrc, Target: testBindDst},
			},
		)
		require.NoError(t, err)
		require.Equal(t, tt.want, resolver.user)
	}
}

func TestWorkspaceIdentityChangeRequiresRecreation(t *testing.T) {
	d := newDriver(newFakeClient(), nil, specDefaults{})
	spec := d.buildSpec(
		wsID,
		&driver.RunOptions{User: rootUser, RemoteUser: testUser},
		nil,
		&mountOwner{UID: 1000, GID: 1000},
	)
	details := toContainerDetails(&sandboxInfo{Labels: spec.Labels})
	for _, user := range []string{testUser, "2000:2001", rootUser} {
		required, _ := d.RequiresRecreate(details, user)
		require.Equal(t, user != testUser, required)
	}
	d.workspaceMountPolicy.StatVirtualization = statVirtOff
	details.Config.Labels[workspaceMountContractLabel] = d.workspaceMountContract()
	required, _ := d.RequiresRecreate(details, rootUser)
	require.False(t, required)
}

func TestDockerlessOwnerNeverLooksUpRunnerAccounts(t *testing.T) {
	for _, user := range []string{testUser, "1000", testNumericIdentity, rootUser} {
		t.Run(user, func(t *testing.T) {
			f := newFakeClient()
			f.info[wsName] = &sandboxInfo{Name: wsName, Running: true}
			d := newDriver(f, nil, specDefaults{})
			resolver := &recordingUserResolver{err: errors.New("runner accounts must not be read")}
			d.userResolver = resolver
			err := d.RunDevContainer(context.Background(), wsID, &driver.RunOptions{
				Image: imgX, User: rootUser, RemoteUser: user, Dockerless: true,
				WorkspaceMount: &config.Mount{Source: testBindSrc, Target: testBindDst},
			})
			if user != rootUser {
				require.ErrorContains(t, err, "non-root Dockerless workspace owner")
				require.Empty(t, f.calls)
				require.True(t, f.info[wsName].Running)
			} else {
				require.NoError(t, err)
				require.Equal(t, &mountOwner{}, f.created[wsName].Mounts[0].Policy.Owner)
			}
			require.Zero(t, resolver.calls)
		})
	}
}

func TestBuildSpecPreservesImageMetadataAndImageUser(t *testing.T) {
	d := newDriver(newFakeClient(), nil, specDefaults{})
	spec := d.buildSpec(wsID, &driver.RunOptions{
		Labels: []string{"devcontainer.metadata=[]", config.UserLabel + "=node"},
		User:   rootUser, RemoteUser: testUser,
	}, nil, nil)
	require.Equal(t, "[]", spec.Labels["devcontainer.metadata"])
	require.Equal(t, "node", spec.Labels[config.UserLabel])
	require.Equal(t, rootUser, spec.Labels[userLabel])
}

func TestWorkspaceWithoutOwnerDoesNotMigrateOnIdentityChange(t *testing.T) {
	d := newDriver(newFakeClient(), nil, specDefaults{})
	spec := d.buildSpec(wsID, &driver.RunOptions{RemoteUser: testUser}, nil, nil)
	required, _ := d.RequiresRecreate(
		toContainerDetails(&sandboxInfo{Labels: spec.Labels}),
		rootUser,
	)
	require.False(t, required)
}
