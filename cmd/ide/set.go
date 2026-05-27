package ide

import (
	"context"
	"fmt"
	"strings"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/ide/ideparse"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/spf13/cobra"
)

// SetCmd holds the set cmd flags.
type SetCmd struct {
	*flags.GlobalFlags

	Options []string
}

// NewSetCmd creates a command that sets the IDE for an existing workspace
// without starting the workspace.
func NewSetCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &SetCmd{
		GlobalFlags: flags,
	}
	setCmd := &cobra.Command{
		Use:   "set [workspace] [ide]",
		Short: "Set the IDE for an existing workspace without starting it",
		Long: `Set the IDE for an existing workspace without starting it.

The change is persisted to the workspace config and will be used on the next
'devsy up'. Available IDEs can be listed with 'devsy ide list'.`,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			if len(args) != 2 {
				return fmt.Errorf("usage: devsy ide set <workspace> <ide>")
			}
			return cmd.Run(cobraCmd.Context(), args[0], args[1])
		},
	}

	setCmd.Flags().
		StringArrayVarP(&cmd.Options, "option", "o", []string{}, "IDE option in the form KEY=VALUE")
	return setCmd
}

// Run runs the command logic.
func (cmd *SetCmd) Run(_ context.Context, workspaceID, ideName string) error {
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}

	contextName := devsyConfig.DefaultContext
	if !provider.WorkspaceExists(contextName, workspaceID) {
		return fmt.Errorf("workspace %q not found in context %q", workspaceID, contextName)
	}

	workspace, err := provider.LoadWorkspaceConfig(contextName, workspaceID)
	if err != nil {
		return fmt.Errorf("load workspace config: %w", err)
	}

	ideName = strings.ToLower(ideName)
	if _, err := ideparse.GetIDEOptions(ideName); err != nil {
		return err
	}

	workspace, err = ideparse.RefreshIDEOptions(devsyConfig, workspace, ideName, cmd.Options)
	if err != nil {
		return fmt.Errorf("refresh ide options: %w", err)
	}

	log.Infof("set IDE for workspace %q to %q", workspace.ID, workspace.IDE.Name)
	return nil
}
