package up

import (
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	probeNone       = "none"
	flagNameMount   = "mount"
	flagMount       = "--" + flagNameMount
	testBindMountAB = "type=bind,source=/a,target=/b"
)

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
	flag := upCmd.Flags().Lookup("default-user-env-probe")
	require.NotNil(t, flag)
	assert.Equal(t, "", flag.DefValue)
}

func TestUpCmd_FlagParsesValue(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	err := upCmd.ParseFlags([]string{"--default-user-env-probe", probeNone})
	require.NoError(t, err)

	flag := upCmd.Flags().Lookup("default-user-env-probe")
	assert.Equal(t, probeNone, flag.Value.String())
}

func TestUpCmd_WorkspaceMountConsistencyFlag(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	flag := upCmd.Flags().Lookup("workspace-mount-consistency")
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
			err := upCmd.ParseFlags([]string{"--workspace-mount-consistency", tt.value})
			require.NoError(t, err)

			flag := upCmd.Flags().Lookup("workspace-mount-consistency")
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
	flag := upCmd.Flags().Lookup("skip-post-create")
	require.NotNil(t, flag)
	assert.Equal(t, "false", flag.DefValue)
}

func TestUpCmd_SkipPostStartFlag(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	flag := upCmd.Flags().Lookup("skip-post-start")
	require.NotNil(t, flag)
	assert.Equal(t, "false", flag.DefValue)
}

func TestUpCmd_SkipPostAttachFlag(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	flag := upCmd.Flags().Lookup("skip-post-attach")
	require.NotNil(t, flag)
	assert.Equal(t, "false", flag.DefValue)
}

func TestUpCmd_SkipHostRequirementsFlag(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	flag := upCmd.Flags().Lookup("skip-host-requirements")
	require.NotNil(t, flag)
	assert.Equal(t, "false", flag.DefValue)
}

func TestUpCmd_SkipFlagsParseValues(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	err := upCmd.ParseFlags([]string{
		"--skip-post-create",
		"--skip-post-start",
		"--skip-post-attach",
		"--skip-host-requirements",
	})
	require.NoError(t, err)

	val, err := upCmd.Flags().GetBool("skip-post-create")
	require.NoError(t, err)
	assert.True(t, val)

	val, err = upCmd.Flags().GetBool("skip-post-start")
	require.NoError(t, err)
	assert.True(t, val)

	val, err = upCmd.Flags().GetBool("skip-post-attach")
	require.NoError(t, err)
	assert.True(t, val)

	val, err = upCmd.Flags().GetBool("skip-host-requirements")
	require.NoError(t, err)
	assert.True(t, val)
}

func TestUpCmd_ContainerUserFlag(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	flag := upCmd.Flags().Lookup("container-user")
	require.NotNil(t, flag)
	assert.Equal(t, "", flag.DefValue)
}

func TestUpCmd_ContainerUserFlagParsesValue(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	err := upCmd.ParseFlags([]string{"--container-user", "devuser"})
	require.NoError(t, err)

	flag := upCmd.Flags().Lookup("container-user")
	assert.Equal(t, "devuser", flag.Value.String())
}

func TestUpCmd_RemoteUserFlag(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	flag := upCmd.Flags().Lookup("remote-user")
	require.NotNil(t, flag)
	assert.Equal(t, "", flag.DefValue)
}

func TestUpCmd_RemoteUserFlagParsesValue(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	err := upCmd.ParseFlags([]string{"--remote-user", "vscode"})
	require.NoError(t, err)

	flag := upCmd.Flags().Lookup("remote-user")
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
