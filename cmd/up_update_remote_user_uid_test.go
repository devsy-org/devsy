package cmd

import (
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpCmd_UpdateRemoteUserUIDDefault_On(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	err := upCmd.ParseFlags(
		[]string{"--update-remote-user-uid-default", UpdateRemoteUserUIDDefaultOn},
	)
	require.NoError(t, err)

	val, err := upCmd.Flags().GetString("update-remote-user-uid-default")
	require.NoError(t, err)
	assert.Equal(t, UpdateRemoteUserUIDDefaultOn, val)
}

func TestUpCmd_UpdateRemoteUserUIDDefault_Off(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	err := upCmd.ParseFlags(
		[]string{"--update-remote-user-uid-default", UpdateRemoteUserUIDDefaultOff},
	)
	require.NoError(t, err)

	val, err := upCmd.Flags().GetString("update-remote-user-uid-default")
	require.NoError(t, err)
	assert.Equal(t, UpdateRemoteUserUIDDefaultOff, val)
}

func TestUpCmd_UpdateRemoteUserUIDDefault_Validate_Invalid(t *testing.T) {
	cmd := &UpCmd{GlobalFlags: &flags.GlobalFlags{}}
	cmd.UpdateRemoteUserUIDDefault = "invalid"
	err := cmd.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid --update-remote-user-uid-default value")
}

func TestUpCmd_UpdateRemoteUserUIDDefault_Validate_Empty(t *testing.T) {
	cmd := &UpCmd{GlobalFlags: &flags.GlobalFlags{}}
	cmd.UpdateRemoteUserUIDDefault = ""
	err := cmd.validate()
	require.NoError(t, err)
}

func TestUpCmd_UpdateRemoteUserUIDDefault_Validate_On(t *testing.T) {
	cmd := &UpCmd{GlobalFlags: &flags.GlobalFlags{}}
	cmd.UpdateRemoteUserUIDDefault = UpdateRemoteUserUIDDefaultOn
	err := cmd.validate()
	require.NoError(t, err)
}

func TestUpCmd_UpdateRemoteUserUIDDefault_Validate_Off(t *testing.T) {
	cmd := &UpCmd{GlobalFlags: &flags.GlobalFlags{}}
	cmd.UpdateRemoteUserUIDDefault = UpdateRemoteUserUIDDefaultOff
	err := cmd.validate()
	require.NoError(t, err)
}
