package pro

import (
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestRoot(t *testing.T) *cobra.Command {
	t.Helper()
	root := &cobra.Command{Use: "devsy"}
	root.AddCommand(NewPlatformCmd(&flags.GlobalFlags{}))
	return root
}

func findCommand(t *testing.T, root *cobra.Command, args ...string) *cobra.Command {
	t.Helper()
	cmd, _, err := root.Find(args)
	require.NoError(t, err)
	return cmd
}

func TestPlatformCommand_NameIsPlatform(t *testing.T) {
	assert.Equal(t, "platform", NewPlatformCmd(&flags.GlobalFlags{}).Name())
}

func TestPlatformCommand_ProAliasResolvesToSameCommand(t *testing.T) {
	root := newTestRoot(t)
	platform := findCommand(t, root, "platform")
	pro := findCommand(t, root, "pro")
	assert.Same(t, platform, pro)
}

func TestPlatformCommand_NestedSubcommandReachableViaBothNames(t *testing.T) {
	root := newTestRoot(t)
	viaPlatform := findCommand(t, root, "platform", "workspace", "list")
	viaPro := findCommand(t, root, "pro", "workspace", "list")
	assert.Same(t, viaPlatform, viaPro)
	assert.Equal(t, "list", viaPlatform.Name())
}
