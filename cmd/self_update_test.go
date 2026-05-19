package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSelfUpdateCmd_CommandName(t *testing.T) {
	cmd := NewSelfUpdateCmd()
	assert.Equal(t, "self-update", cmd.Use)
}

func TestNewSelfUpdateCmd_HasVersionFlag(t *testing.T) {
	cmd := NewSelfUpdateCmd()
	f := cmd.Flags().Lookup("version")
	require.NotNil(t, f, "--version flag must exist")
	assert.Equal(t, "", f.DefValue)
}

func TestNewSelfUpdateCmd_HasDryRunFlag(t *testing.T) {
	cmd := NewSelfUpdateCmd()
	f := cmd.Flags().Lookup("dry-run")
	require.NotNil(t, f, "--dry-run flag must exist")
	assert.Equal(t, "false", f.DefValue)
}

func TestNewSelfUpdateCmd_HasChannelFlag(t *testing.T) {
	cmd := NewSelfUpdateCmd()
	f := cmd.Flags().Lookup("channel")
	require.NotNil(t, f, "--channel flag must exist")
	assert.Equal(t, "stable", f.DefValue)
}

func TestNewSelfUpdateCmd_AcceptsNoPositionalArgs(t *testing.T) {
	cmd := NewSelfUpdateCmd()
	err := cmd.Args(cmd, []string{"unexpected"})
	assert.Error(t, err, "should reject positional arguments")

	err = cmd.Args(cmd, []string{})
	assert.NoError(t, err, "should accept zero arguments")
}

func TestNewSelfUpdateCmd_RejectsInvalidChannel(t *testing.T) {
	cmd := NewSelfUpdateCmd()
	cmd.SetArgs([]string{"--channel", "nightly"})
	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid channel")
}

func TestNewSelfUpdateCmd_AcceptsValidChannels(t *testing.T) {
	for _, ch := range []string{"stable", "beta"} {
		cmd := NewSelfUpdateCmd()
		require.NoError(t, cmd.Flags().Set("channel", ch))
		// PreRunE should pass validation
		preRunE := cmd.PreRunE
		require.NotNil(t, preRunE)
		assert.NoError(t, preRunE(cmd, nil))
	}
}
