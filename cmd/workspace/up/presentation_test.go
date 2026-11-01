package up

import (
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testDetachFlag   = "--detach"
	testDisplayLabel = "My Label"
)

func TestDetachedArgsKeepPresentationFlags(t *testing.T) {
	got := detachedArgs([]string{
		testRepoArg,
		testDetachFlag,
		names.Flag(names.DisplayName), testDisplayLabel,
		names.FlagValue(names.SSHHostAlias, "short"),
	})

	assert.Equal(t, []string{
		testRepoArg,
		names.Flag(names.DisplayName), testDisplayLabel,
		names.FlagValue(names.SSHHostAlias, "short"),
	}, got)
}

func TestUpCmdRegistersPresentationFlags(t *testing.T) {
	cmd := NewUpCmd(&flags.GlobalFlags{})

	require.NoError(t, cmd.ParseFlags([]string{
		names.Flag(names.DisplayName), testDisplayLabel,
		names.Flag(names.SSHHostAlias), "short",
	}))
	display, err := cmd.Flags().GetString(names.DisplayName)
	require.NoError(t, err)
	alias, err := cmd.Flags().GetString(names.SSHHostAlias)
	require.NoError(t, err)
	assert.Equal(t, testDisplayLabel, display)
	assert.Equal(t, "short", alias)
}

func TestCheckPresentationSkipsWhenUnsetOrExisting(t *testing.T) {
	cfg := &config.Config{}

	unset := &UpCmd{}
	require.NoError(t, unset.checkPresentation(cfg, "ws", false))

	existing := &UpCmd{DisplayName: "Label", SSHHostAlias: "short"}
	require.NoError(t, existing.checkPresentation(cfg, "ws", true))
	require.NoError(t, existing.checkPresentation(cfg, "", false))
}

func TestTargetWorkspaceID(t *testing.T) {
	cmd := &UpCmd{}
	assert.Empty(t, cmd.targetWorkspaceID(nil))

	cmd.ID = "explicit"
	assert.Equal(t, "explicit", cmd.targetWorkspaceID([]string{"other"}))
}
