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

	Options   []string
	Workspace string
}

// NewSetCmd creates the 'devsy ide set' command. Without --workspace it sets
// global options for the named IDE; with --workspace it assigns the IDE to an
// existing workspace without starting it.
func NewSetCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &SetCmd{
		GlobalFlags: flags,
	}
	setCmd := &cobra.Command{
		Use:   "set <ide>",
		Short: "Set IDE options, or assign an IDE to a workspace with --workspace",
		Long: `Set IDE options for the named IDE.

With --workspace <name>, assigns the IDE to an existing workspace without
starting it. The change is persisted to the workspace config and used on the
next 'devsy workspace up'. Available IDEs can be listed with 'devsy ide list'.`,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("please specify the ide")
			}
			return cmd.Run(cobraCmd.Context(), args[0])
		},
	}

	setCmd.Flags().
		StringArrayVarP(&cmd.Options, "option", "o", []string{}, "IDE option in the form KEY=VALUE")
	setCmd.Flags().
		StringVar(&cmd.Workspace, "workspace", "", "Assign the IDE to this workspace instead of setting global options")
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

	if cmd.Workspace != "" {
		return cmd.runWorkspace(devsyConfig, ideName)
	}

	if len(cmd.Options) > 0 {
		if err := setOptions(devsyConfig, ideName, cmd.Options, ideOptions); err != nil {
			return err
		}
	}

	if err := config.SaveConfig(devsyConfig); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}

func (cmd *SetCmd) runWorkspace(devsyConfig *config.Config, ideName string) error {
	contextName := devsyConfig.DefaultContext
	if !provider.WorkspaceExists(contextName, cmd.Workspace) {
		return fmt.Errorf("workspace %q not found in context %q", cmd.Workspace, contextName)
	}

	workspace, err := provider.LoadWorkspaceConfig(contextName, cmd.Workspace)
	if err != nil {
		return fmt.Errorf("load workspace config: %w", err)
	}

	workspace, err = ideparse.RefreshIDEOptions(devsyConfig, workspace, ideName, cmd.Options)
	if err != nil {
		return fmt.Errorf("refresh ide options: %w", err)
	}

	log.Infof("set IDE for workspace %q to %q", workspace.ID, workspace.IDE.Name)
	return nil
}
