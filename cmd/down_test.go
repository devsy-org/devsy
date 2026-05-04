package cmd

import (
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDownCmd_RemoveVolumesFlag(t *testing.T) {
	downCmd := NewDownCmd(&flags.GlobalFlags{})
	err := downCmd.ParseFlags([]string{"--remove-volumes"})
	require.NoError(t, err)

	val, err := downCmd.Flags().GetBool("remove-volumes")
	require.NoError(t, err)
	assert.True(t, val)
}

func TestDownCmd_RemoveVolumesFlag_DefaultFalse(t *testing.T) {
	downCmd := NewDownCmd(&flags.GlobalFlags{})
	err := downCmd.ParseFlags([]string{})
	require.NoError(t, err)

	val, err := downCmd.Flags().GetBool("remove-volumes")
	require.NoError(t, err)
	assert.False(t, val)
}
