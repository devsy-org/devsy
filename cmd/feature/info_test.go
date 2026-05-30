package feature

import (
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInfoCmd_FlagDefaults(t *testing.T) {
	cmd := NewInfoCmd(nil)

	showTagsFlag := cmd.Flags().Lookup("show-tags")
	require.NotNil(t, showTagsFlag)
	assert.Equal(t, "false", showTagsFlag.DefValue)

	showDepsFlag := cmd.Flags().Lookup("show-dependencies")
	require.NotNil(t, showDepsFlag)
	assert.Equal(t, "false", showDepsFlag.DefValue)
}

func TestInfoCmd_AllFlagsRegistered(t *testing.T) {
	cmd := NewInfoCmd(nil)
	expected := []string{"show-tags", "show-dependencies"}
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

func TestInfoCmd_InvalidFeatureReference(t *testing.T) {
	infoCmd := &InfoCmd{
		GlobalFlags: &flags.GlobalFlags{ResultFormat: output.ModePlain},
	}
	err := infoCmd.Run("not a valid reference!!!")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid feature reference")
}

func TestInfoCmd_HasSubcommands(t *testing.T) {
	cmd := NewInfoCmd(nil)
	subcommands := make(map[string]bool)
	for _, sub := range cmd.Commands() {
		subcommands[sub.Name()] = true
	}
	assert.True(t, subcommands["manifest"], "info should have 'manifest' subcommand")
	assert.True(t, subcommands["tags"], "info should have 'tags' subcommand")
}
