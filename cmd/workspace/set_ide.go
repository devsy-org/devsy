package workspace

import (
	"context"
	"fmt"
	"strings"

	"github.com/devsy-org/devsy/cmd/completion"
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/ide/ideparse"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/spf13/cobra"
)

// SetIDECmd holds the set-ide cmd flags.
type SetIDECmd struct {
	*flags.GlobalFlags

	Options []string
}

// NewSetIDECmd creates the 'devsy workspace set-ide' command. It assigns an
// IDE to an existing workspace without starting it. The change is persisted
// to the workspace config and used on the next 'devsy workspace up'.
func NewSetIDECmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &SetIDECmd{
		GlobalFlags: globalFlags,
	}

	setIDECmd := &cobra.Command{
		Use:   "set-ide <workspace> <ide>",
		Short: "Set a workspace's IDE",
		Long: `Set a workspace's IDE, persisted to its config and applied on the next
'devsy workspace up'. List available IDEs with 'devsy ide list'.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args[0], args[1])
		},
		ValidArgsFunction: func(
			rootCmd *cobra.Command, args []string, toComplete string,
		) ([]string, cobra.ShellCompDirective) {
			if len(args) == 0 {
				return completion.GetWorkspaceSuggestions(
					rootCmd,
					cmd.Context,
					cmd.Provider,
					args,
					toComplete,
					cmd.Owner,
				)
			}
			return nil, cobra.ShellCompDirectiveNoFileComp
		},
	}

	cliflags.Add(
		setIDECmd,
		cliflags.StringArray(&cmd.Options, names.Option, nil, "IDE option in the form KEY=VALUE").
			Shorthand("o"),
	)
	return setIDECmd
}

// Run validates inputs and applies the IDE assignment to the workspace config.
func (cmd *SetIDECmd) Run(_ context.Context, workspaceName, ideName string) error {
	ideName = strings.ToLower(ideName)

	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}

	if _, err := ideparse.GetIDEOptions(ideName); err != nil {
		return err
	}

	contextName := devsyConfig.DefaultContext
	if !provider.WorkspaceExists(contextName, workspaceName) {
		return fmt.Errorf("workspace %q not found in context %q", workspaceName, contextName)
	}

	workspace, err := provider.LoadWorkspaceConfig(contextName, workspaceName)
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
