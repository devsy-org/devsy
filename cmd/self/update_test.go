package self

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewUpdateCmd_CommandName(t *testing.T) {
	cmd := NewUpdateCmd()
	assert.Equal(t, "update", cmd.Use)
}

func TestNewUpdateCmd_HasVersionFlag(t *testing.T) {
	cmd := NewUpdateCmd()
	f := cmd.Flags().Lookup("version")
	require.NotNil(t, f, "--version flag must exist")
	assert.Equal(t, "", f.DefValue)
}

func TestNewUpdateCmd_HasDryRunFlag(t *testing.T) {
	cmd := NewUpdateCmd()
	f := cmd.Flags().Lookup("dry-run")
	require.NotNil(t, f, "--dry-run flag must exist")
	assert.Equal(t, "false", f.DefValue)
}

func TestNewUpdateCmd_HasChannelFlag(t *testing.T) {
	cmd := NewUpdateCmd()
	f := cmd.Flags().Lookup("channel")
	require.NotNil(t, f, "--channel flag must exist")
	assert.Equal(t, channelStable, f.DefValue)
}

func TestNewUpdateCmd_AcceptsNoPositionalArgs(t *testing.T) {
	cmd := NewUpdateCmd()
	err := cmd.Args(cmd, []string{"unexpected"})
	assert.Error(t, err, "should reject positional arguments")

	err = cmd.Args(cmd, []string{})
	assert.NoError(t, err, "should accept zero arguments")
}

func TestNewUpdateCmd_RejectsInvalidChannel(t *testing.T) {
	cmd := NewUpdateCmd()
	cmd.SetArgs([]string{"--channel", "nightly"})
	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid channel")
}

func TestNewUpdateCmd_AcceptsValidChannels(t *testing.T) {
	for _, ch := range []string{channelStable, channelBeta} {
		cmd := NewUpdateCmd()
		require.NoError(t, cmd.Flags().Set("channel", ch))
		preRunE := cmd.PreRunE
		require.NotNil(t, preRunE)
		assert.NoError(t, preRunE(cmd, nil))
	}
}
