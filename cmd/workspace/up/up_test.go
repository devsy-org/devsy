package up

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/ide/opener"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	snapshotpkg "github.com/devsy-org/devsy/pkg/snapshot"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	probeNone       = "none"
	flagNameMount   = "mount"
	flagMount       = "--" + flagNameMount
	testBindMountAB = "type=bind,source=/a,target=/b"
	testSnapshotRef = "ghcr.io/acme/s:my-ws-20260731150405-abcxyz"
)

func TestUpCmd_NoLockfileAndFrozenLockfileMutuallyExclusive(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	require.NoError(t, upCmd.Flags().Parse(
		[]string{names.Flag(names.NoLockfile), names.Flag(names.FrozenLockfile)},
	))
	err := upCmd.ValidateFlagGroups()
	require.Error(t, err)
	assert.Contains(t, err.Error(), names.NoLockfile)
	assert.Contains(t, err.Error(), names.FrozenLockfile)
}

func TestUpCmd_ValidateDefaultUserEnvProbe(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "empty is valid", value: "", wantErr: false},
		{name: "none", value: probeNone, wantErr: false},
		{name: "loginShell", value: "loginShell", wantErr: false},
		{name: "interactiveShell", value: "interactiveShell", wantErr: false},
		{name: "loginInteractiveShell", value: "loginInteractiveShell", wantErr: false},
		{name: "invalid value", value: "bogus", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &UpCmd{
				GlobalFlags: &flags.GlobalFlags{},
			}
			cmd.DefaultUserEnvProbe = tt.value
			err := cmd.validate()
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "invalid userEnvProbe")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestUpCmd_FlagRegistered(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	flag := upCmd.Flags().Lookup(names.UserEnvProbe)
	require.NotNil(t, flag)
	assert.Equal(t, "", flag.DefValue)
}

func TestUpCmd_FlagParsesValue(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	err := upCmd.ParseFlags([]string{names.Flag(names.UserEnvProbe), probeNone})
	require.NoError(t, err)

	flag := upCmd.Flags().Lookup(names.UserEnvProbe)
	assert.Equal(t, probeNone, flag.Value.String())
}

func TestUpCmd_WorkspaceMountConsistencyFlag(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	flag := upCmd.Flags().Lookup(names.WorkspaceMountConsistency)
	require.NotNil(t, flag)
	assert.Equal(t, "", flag.DefValue)
}

func TestUpCmd_WorkspaceMountConsistencyFlagParsesValue(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "consistent", value: MountConsistencyConsistent},
		{name: "cached", value: MountConsistencyCached},
		{name: "delegated", value: MountConsistencyDelegated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upCmd := NewUpCmd(&flags.GlobalFlags{})
			err := upCmd.ParseFlags(
				[]string{names.Flag(names.WorkspaceMountConsistency), tt.value},
			)
			require.NoError(t, err)

			flag := upCmd.Flags().Lookup(names.WorkspaceMountConsistency)
			assert.Equal(t, tt.value, flag.Value.String())
		})
	}
}

func TestUpCmd_ValidateWorkspaceMountConsistency(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "empty is valid", value: "", wantErr: false},
		{name: "consistent", value: MountConsistencyConsistent, wantErr: false},
		{name: "cached", value: MountConsistencyCached, wantErr: false},
		{name: "delegated", value: MountConsistencyDelegated, wantErr: false},
		{name: "invalid value", value: "bogus", wantErr: true},
		{name: "partial match", value: "cache", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &UpCmd{GlobalFlags: &flags.GlobalFlags{}}
			cmd.WorkspaceMountConsistency = tt.value
			err := cmd.validate()
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "invalid --workspace-mount-consistency")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestUpCmd_SkipPostCreateFlag(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	flag := upCmd.Flags().Lookup(names.SkipPostCreate)
	require.NotNil(t, flag)
	assert.Equal(t, "false", flag.DefValue)
}

func TestUpCmd_SkipPostStartFlag(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	flag := upCmd.Flags().Lookup(names.SkipPostStart)
	require.NotNil(t, flag)
	assert.Equal(t, "false", flag.DefValue)
}

func TestUpCmd_SkipPostAttachFlag(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	flag := upCmd.Flags().Lookup(names.SkipPostAttach)
	require.NotNil(t, flag)
	assert.Equal(t, "false", flag.DefValue)
}

func TestUpCmd_SkipHostRequirementsFlag(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	flag := upCmd.Flags().Lookup(names.SkipHostRequirements)
	require.NotNil(t, flag)
	assert.Equal(t, "false", flag.DefValue)
}

func TestUpCmd_SkipFlagsParseValues(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	err := upCmd.ParseFlags([]string{
		names.Flag(names.SkipPostCreate),
		names.Flag(names.SkipPostStart),
		names.Flag(names.SkipPostAttach),
		names.Flag(names.SkipHostRequirements),
	})
	require.NoError(t, err)

	val, err := upCmd.Flags().GetBool(names.SkipPostCreate)
	require.NoError(t, err)
	assert.True(t, val)

	val, err = upCmd.Flags().GetBool(names.SkipPostStart)
	require.NoError(t, err)
	assert.True(t, val)

	val, err = upCmd.Flags().GetBool(names.SkipPostAttach)
	require.NoError(t, err)
	assert.True(t, val)

	val, err = upCmd.Flags().GetBool(names.SkipHostRequirements)
	require.NoError(t, err)
	assert.True(t, val)
}

func TestUpCmd_ContainerUserFlag(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	flag := upCmd.Flags().Lookup(names.ContainerUser)
	require.NotNil(t, flag)
	assert.Equal(t, "", flag.DefValue)
}

func TestUpCmd_ContainerUserFlagParsesValue(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	err := upCmd.ParseFlags([]string{names.Flag(names.ContainerUser), "devuser"})
	require.NoError(t, err)

	flag := upCmd.Flags().Lookup(names.ContainerUser)
	assert.Equal(t, "devuser", flag.Value.String())
}

func TestUpCmd_RemoteUserFlag(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	flag := upCmd.Flags().Lookup(names.RemoteUser)
	require.NotNil(t, flag)
	assert.Equal(t, "", flag.DefValue)
}

func TestUpCmd_RemoteUserFlagParsesValue(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	err := upCmd.ParseFlags([]string{names.Flag(names.RemoteUser), "vscode"})
	require.NoError(t, err)

	flag := upCmd.Flags().Lookup(names.RemoteUser)
	assert.Equal(t, "vscode", flag.Value.String())
}

func TestUpCmd_MountFlag(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	flag := upCmd.Flags().Lookup(flagNameMount)
	require.NotNil(t, flag)
	assert.Equal(t, "[]", flag.DefValue)
}

func TestUpCmd_MountFlagParsesValue(t *testing.T) {
	const bindMount = "type=bind,source=/host/path,target=/container/path"
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	err := upCmd.ParseFlags([]string{flagMount, bindMount})
	require.NoError(t, err)

	flag := upCmd.Flags().Lookup(flagNameMount)
	assert.Contains(t, flag.Value.String(), bindMount)
}

func TestUpCmd_MountFlagRepeatable(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	err := upCmd.ParseFlags([]string{
		flagMount, testBindMountAB,
		flagMount, "type=volume,source=myvolume,target=/c",
	})
	require.NoError(t, err)

	flag := upCmd.Flags().Lookup(flagNameMount)
	val := flag.Value.String()
	assert.Contains(t, val, testBindMountAB)
	assert.Contains(t, val, "type=volume,source=myvolume,target=/c")
}

func TestUpCmd_ValidateMounts(t *testing.T) {
	tests := []struct {
		name    string
		mounts  []string
		wantErr bool
	}{
		{name: "empty is valid", mounts: []string{}},
		{
			name:    "valid bind mount",
			mounts:  []string{"type=bind,source=/host,target=/container"},
			wantErr: false,
		},
		{
			name:    "valid volume mount",
			mounts:  []string{"type=volume,source=vol,target=/data"},
			wantErr: false,
		},
		{name: "multiple valid", mounts: []string{
			testBindMountAB,
			"type=volume,source=v,target=/c",
		}, wantErr: false},
		{name: "missing target", mounts: []string{"type=bind,source=/host"}, wantErr: true},
		{name: "one valid one missing target", mounts: []string{
			testBindMountAB,
			"type=bind,source=/c",
		}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &UpCmd{GlobalFlags: &flags.GlobalFlags{}}
			cmd.Mounts = tt.mounts
			err := cmd.validate()
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "invalid --mount")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestBuildUpCmd_AppliesOptions(t *testing.T) {
	g := &flags.GlobalFlags{Provider: "default-provider", ResultFormat: ""}
	opts := Options{
		Source:           "github.com/example/repo",
		Name:             "my-ws",
		Provider:         "k8s",
		IDE:              "vscode",
		DevcontainerPath: ".devcontainer/devcontainer.json",
	}
	cmd := buildUpCmd(g, opts)

	assert.Equal(t, "vscode", cmd.IDE)
	assert.Equal(t, ".devcontainer/devcontainer.json", cmd.DevContainerPath)
	assert.Equal(t, "my-ws", cmd.ID)
	assert.Equal(t, "k8s", cmd.Provider, "Provider override must reach LoadConfig via gCopy")
	assert.Equal(t, "plain", cmd.ResultFormat, "default ResultFormat ensures human-readable output")
	require.NotNil(t, cmd.Out, "Out must be set to suppress JSON envelope writes to stdout")
	assert.Equal(t, "default-provider", g.Provider, "caller's GlobalFlags must not be mutated")
}

func TestBuildUpCmd_DefaultsIDEToNone(t *testing.T) {
	g := &flags.GlobalFlags{}
	cmd := buildUpCmd(g, Options{Source: "src"})
	assert.Equal(
		t,
		"none",
		cmd.IDE,
		"MCP path must default IDE to none — there's no human to attach an IDE to",
	)
}

func TestUpCmd_FromSnapshotSetsSource(t *testing.T) {
	cmd := &UpCmd{FromSnapshot: testSnapshotRef}
	src, err := cmd.resolveExplicitSource()
	require.NoError(t, err)
	require.NotNil(t, src)
	require.Equal(t, testSnapshotRef, src.Snapshot)
}

func TestUpCmd_FromSnapshotSetsDevContainerSource(t *testing.T) {
	// Parity check: --from-snapshot must compose the exact same
	// DevContainerSource override that `devsy snapshot restore` uses
	// (pkg/snapshot.RestoreComposition), so both entry points restore
	// identically.
	cmd := &UpCmd{FromSnapshot: testSnapshotRef}
	_, err := cmd.resolveExplicitSource()
	require.NoError(t, err)
	assert.Equal(t, "image:"+testSnapshotRef+"-fs", cmd.DevContainerSource)
}

func TestUpCmd_FromSnapshotDerivesWorkspaceIDWhenIDUnset(t *testing.T) {
	cmd := &UpCmd{FromSnapshot: testSnapshotRef}
	_, err := cmd.resolveExplicitSource()
	require.NoError(t, err)
	assert.Equal(t, "my-ws", cmd.ID)
}

func TestUpCmd_FromSnapshotDoesNotOverrideExplicitID(t *testing.T) {
	cmd := &UpCmd{FromSnapshot: testSnapshotRef}
	cmd.ID = "explicit-id"
	_, err := cmd.resolveExplicitSource()
	require.NoError(t, err)
	assert.Equal(t, "explicit-id", cmd.ID)
}

func TestUpCmd_ResolveExplicitSourceNilWhenUnset(t *testing.T) {
	cmd := &UpCmd{}
	src, err := cmd.resolveExplicitSource()
	require.NoError(t, err)
	require.Nil(t, src)
}

func TestUpCmd_ResolveExplicitSourceErrorsOnBadRef(t *testing.T) {
	cmd := &UpCmd{FromSnapshot: "not a valid ref!!"}
	_, err := cmd.resolveExplicitSource()
	require.Error(t, err)
}

func TestUpCmd_ValidateFromSnapshotConflictsWithSourceFlag(t *testing.T) {
	cmd := &UpCmd{FromSnapshot: testSnapshotRef}
	cmd.Source = "git:https://github.com/acme/example"
	err := cmd.validateFromSnapshot(context.Background(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot combine --from-snapshot with an explicit source")
}

func TestUpCmd_ValidateFromSnapshotConflictsWithPositionalArg(t *testing.T) {
	cmd := &UpCmd{FromSnapshot: testSnapshotRef}
	err := cmd.validateFromSnapshot(context.Background(), []string{"some-workspace"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot combine --from-snapshot with an explicit source")
}

func TestUpCmd_ValidateFromSnapshotNoOpWhenUnset(t *testing.T) {
	cmd := &UpCmd{}
	require.NoError(t, cmd.validateFromSnapshot(context.Background(), []string{"some-workspace"}))
}

func TestUpCmd_ValidateFromSnapshotRejectsPlatformMode(t *testing.T) {
	cmd := &UpCmd{FromSnapshot: testSnapshotRef}
	cmd.Platform.Enabled = true
	err := cmd.validateFromSnapshot(context.Background(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "platform mode")
}

func TestUpCmd_ValidateFromSnapshotFailsOnMalformedRef(t *testing.T) {
	cmd := &UpCmd{FromSnapshot: "not a valid ref!!"}
	err := cmd.validateFromSnapshot(context.Background(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "validate --from-snapshot ref")
}

func TestUpCmd_ValidateFromSnapshotFailsOnMissingManifest(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	cmd := &UpCmd{FromSnapshot: host + "/acme/snapshots:my-ws-20260731150405"}
	err := cmd.validateFromSnapshot(context.Background(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "validate --from-snapshot ref")
}

func TestUpCmd_ApplyFromSnapshotOverridesKeepsExplicitRemoteUserFlag(t *testing.T) {
	cmd := &UpCmd{CLIOptions: provider2.CLIOptions{RemoteUser: "explicit-user"}}
	manifest := mustBuildManifest(t, "snapshot-user")

	require.NoError(t, cmd.applyFromSnapshotOverrides(manifest))

	assert.Equal(t, "explicit-user", cmd.RemoteUser)
}

func TestUpCmd_ApplyFromSnapshotOverridesUsesSnapshotRemoteUserWhenUnset(t *testing.T) {
	cmd := &UpCmd{}
	manifest := mustBuildManifest(t, "snapshot-user")

	require.NoError(t, cmd.applyFromSnapshotOverrides(manifest))

	assert.Equal(t, "snapshot-user", cmd.RemoteUser)
}

func mustBuildManifest(t *testing.T, remoteUser string) *snapshotpkg.Manifest {
	t.Helper()
	manifest, err := snapshotpkg.BuildManifest(snapshotpkg.BuildManifestOptions{
		WorkspaceUID:         "ws-uid",
		ContainerImageDigest: "sha256:" + strings.Repeat("a", 64),
		VolumesDigest:        "sha256:" + strings.Repeat("b", 64),
		RemoteUser:           remoteUser,
	})
	require.NoError(t, err)
	return manifest
}

func TestBuildUpCmd_DoesNotMutateCallerGlobalFlags(t *testing.T) {
	g := &flags.GlobalFlags{Provider: "default-provider", ResultFormat: ""}

	// Two calls with different overrides must each see a clean copy. If the
	// shallow-copy guard regressed, the second call would inherit the first
	// call's override.
	first := buildUpCmd(g, Options{Source: "src1", Provider: "alpha"})
	second := buildUpCmd(g, Options{Source: "src2", Provider: "beta"})

	assert.Equal(t, "alpha", first.Provider, "first call applies its own override")
	assert.Equal(t, "beta", second.Provider, "second call applies its own override")
	assert.Equal(t, "default-provider", g.Provider, "caller's Provider must remain untouched")
	assert.Equal(
		t,
		"",
		g.ResultFormat,
		"caller's ResultFormat must remain untouched even after copy defaulted it",
	)
}

func TestUpCmd_IDELaunchSkipImpliesIDENoneWhenIDEUnset(t *testing.T) {
	cmd := &UpCmd{GlobalFlags: &flags.GlobalFlags{}}
	cmd.IDELaunch = opener.LaunchSkip
	cmd.IDE = ""

	err := cmd.validate()
	require.NoError(t, err)
	assert.Equal(t, string(config.IDENone), cmd.IDE,
		"ide-launch=skip with no explicit --ide should default IDE to none, "+
			"so the container never downloads an IDE server binary")
}

func TestUpCmd_IDELaunchSkipRespectsExplicitIDE(t *testing.T) {
	cmd := &UpCmd{GlobalFlags: &flags.GlobalFlags{}}
	cmd.IDELaunch = opener.LaunchSkip
	cmd.IDE = "openvscode"

	err := cmd.validate()
	require.NoError(t, err)
	assert.Equal(t, "openvscode", cmd.IDE,
		"an explicit --ide value must not be overridden even when launch is skipped")
}

func TestUpCmd_IDELaunchAutoDoesNotTouchIDE(t *testing.T) {
	cmd := &UpCmd{GlobalFlags: &flags.GlobalFlags{}}
	cmd.IDELaunch = opener.LaunchAuto
	cmd.IDE = ""

	err := cmd.validate()
	require.NoError(t, err)
	assert.Equal(t, "", cmd.IDE, "auto launch must not force an IDE default in validate()")
}
