package workspace

import (
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildCmd_NoCacheFlag(t *testing.T) {
	buildCmd := NewBuildCmd(&flags.GlobalFlags{})
	flag := buildCmd.Flags().Lookup(names.NoCache)
	require.NotNil(t, flag)
	assert.Equal(t, "false", flag.DefValue)
}

func TestBuildCmd_NoCacheFlagParsesValue(t *testing.T) {
	buildCmd := NewBuildCmd(&flags.GlobalFlags{})
	err := buildCmd.ParseFlags([]string{"--no-cache"})
	require.NoError(t, err)

	val, err := buildCmd.Flags().GetBool(names.NoCache)
	require.NoError(t, err)
	assert.True(t, val)
}

func TestBuildCmd_ImageNameFlag(t *testing.T) {
	buildCmd := NewBuildCmd(&flags.GlobalFlags{})
	flag := buildCmd.Flags().Lookup(names.ImageName)
	require.NotNil(t, flag)
	assert.Equal(t, "", flag.DefValue)
}

func TestBuildCmd_ImageNameFlagParsesValue(t *testing.T) {
	buildCmd := NewBuildCmd(&flags.GlobalFlags{})
	err := buildCmd.ParseFlags([]string{"--image-name", "my-custom-image:latest"})
	require.NoError(t, err)

	flag := buildCmd.Flags().Lookup(names.ImageName)
	assert.Equal(t, "my-custom-image:latest", flag.Value.String())
}

func TestBuildCmd_NoBuildFlag(t *testing.T) {
	buildCmd := NewBuildCmd(&flags.GlobalFlags{})
	flag := buildCmd.Flags().Lookup(names.NoBuild)
	require.NotNil(t, flag)
	assert.Equal(t, "false", flag.DefValue)
}

func TestBuildCmd_NoBuildFlagParsesValue(t *testing.T) {
	buildCmd := NewBuildCmd(&flags.GlobalFlags{})
	err := buildCmd.ParseFlags([]string{"--no-build"})
	require.NoError(t, err)

	val, err := buildCmd.Flags().GetBool(names.NoBuild)
	require.NoError(t, err)
	assert.True(t, val)
}

func TestBuildCmd_PushAndSkipPushMutuallyExclusive(t *testing.T) {
	buildCmd := NewBuildCmd(&flags.GlobalFlags{})
	buildCmd.SetArgs([]string{"--" + names.Push, "--" + names.SkipPush})
	err := buildCmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "none of the others can be")
}

func TestBuildCmd_NoCacheDefaultFalse(t *testing.T) {
	buildCmd := NewBuildCmd(&flags.GlobalFlags{})
	err := buildCmd.ParseFlags([]string{})
	require.NoError(t, err)

	val, err := buildCmd.Flags().GetBool(names.NoCache)
	require.NoError(t, err)
	assert.False(t, val)
}
