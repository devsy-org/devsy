package ide

import (
	"context"
	"fmt"
	"strings"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/ide/ideparse"
	"github.com/spf13/cobra"
)

// SetCmd holds the set cmd flags.
type SetCmd struct {
	*flags.GlobalFlags

	Options []string
}

// NewSetCmd creates the 'devsy ide set' command. It sets global options for
// the named IDE. To assign an IDE to a specific workspace, use
// 'devsy workspace set-ide <workspace> <ide>'.
func NewSetCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &SetCmd{
		GlobalFlags: flags,
	}
	setCmd := &cobra.Command{
		Use:   "set <ide>",
		Short: "Set global IDE options",
		Long: `Set global options for the named IDE.

To assign an IDE to a specific workspace, use
'devsy workspace set-ide <workspace> <ide>'. Available IDEs can be listed
with 'devsy ide list'.`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: ideNameCompletion,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			if len(cmd.Options) == 0 {
				return fmt.Errorf("nothing to do: pass --option KEY=VALUE")
			}
			return cmd.Run(cobraCmd.Context(), args[0])
		},
	}

	setCmd.Flags().
		StringArrayVarP(&cmd.Options, "option", "o", []string{}, "IDE option in the form KEY=VALUE")
	return setCmd
}

// Run runs the command logic.
func (cmd *SetCmd) Run(_ context.Context, ideName string) error {
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}

	ideName = strings.ToLower(ideName)
	ideOptions, err := ideparse.GetIDEOptions(ideName)
	if err != nil {
		return err
	}

	if err := setOptions(devsyConfig, ideName, cmd.Options, ideOptions); err != nil {
		return err
	}

	if err := config.SaveConfig(devsyConfig); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}
