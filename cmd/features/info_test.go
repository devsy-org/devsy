package features

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInfoCmd_FlagDefaults(t *testing.T) {
	cmd := NewInfoCmd(nil)

	outputFlag := cmd.Flags().Lookup("output")
	require.NotNil(t, outputFlag)
	assert.Equal(t, "text", outputFlag.DefValue)

	showTagsFlag := cmd.Flags().Lookup("show-tags")
	require.NotNil(t, showTagsFlag)
	assert.Equal(t, "false", showTagsFlag.DefValue)

	showDepsFlag := cmd.Flags().Lookup("show-dependencies")
	require.NotNil(t, showDepsFlag)
	assert.Equal(t, "false", showDepsFlag.DefValue)
}

func TestInfoCmd_AllFlagsRegistered(t *testing.T) {
	cmd := NewInfoCmd(nil)
	expected := []string{"output", "show-tags", "show-dependencies"}
	for _, name := range expected {
		assert.NotNil(t, cmd.Flags().Lookup(name), "flag %q should be registered", name)
	}
}

func TestInfoCmd_RequiresExactlyOneArg(t *testing.T) {
	cmd := NewInfoCmd(nil)
	assert.NotNil(t, cmd.Args)
	assert.Error(t, cmd.Args(cmd, []string{}))
	assert.NoError(t, cmd.Args(cmd, []string{"one"}))
	assert.Error(t, cmd.Args(cmd, []string{"one", "two"}))
}

func TestInfoCmd_InvalidOutputFormat(t *testing.T) {
	infoCmd := &InfoCmd{
		Output: "yaml",
	}
	err := infoCmd.Run("ghcr.io/devcontainers/features/go:1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid output format")
	assert.Contains(t, err.Error(), "yaml")
}

func TestInfoCmd_InvalidFeatureReference(t *testing.T) {
	infoCmd := &InfoCmd{
		Output: "text",
	}
	err := infoCmd.Run("not a valid reference!!!")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid feature reference")
}
