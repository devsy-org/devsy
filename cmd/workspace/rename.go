package workspace

import (
	"context"
	"fmt"
	"strings"

	"github.com/devsy-org/devsy/cmd/completion"
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	workspace "github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// RenameCmd holds the rename cmd flags.
type RenameCmd struct {
	*flags.GlobalFlags
}

// NewRenameCmd creates a new command for renaming a workspace.
func NewRenameCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &RenameCmd{
		GlobalFlags: globalFlags,
	}

	return &cobra.Command{
		Use:     "rename <current-name> <new-name>",
		Aliases: []string{"mv"},
		Short:   "Rename a workspace",
		Args:    cobra.ExactArgs(2),
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args)
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
}

// Run validates inputs, loads config, and executes the workspace rename.
func (cmd *RenameCmd) Run(ctx context.Context, args []string) error {
	oldName, newName := args[0], args[1]

	if oldName == newName {
		return fmt.Errorf("new name is the same as the current name")
	}

	if err := validateWorkspaceName(newName); err != nil {
		return err
	}

	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}

	if err := validateWorkspaceExists(devsyConfig, oldName); err != nil {
		return err
	}

	if workspace.Exists(ctx, devsyConfig, []string{newName}, "", cmd.Owner) != "" {
		return fmt.Errorf("workspace %s already exists", newName)
	}

	return workspace.Rename(ctx, workspace.RenameOptions{
		DevsyConfig: devsyConfig,
		OldName:     oldName,
		NewName:     newName,
	})
}

func validateWorkspaceName(newName string) error {
	if strings.TrimSpace(newName) == "" {
		return fmt.Errorf("workspace name cannot be empty")
	}
	if provider.ProviderNameRegEx.MatchString(newName) {
		return fmt.Errorf("workspace name can only include lowercase letters, numbers or dashes")
	}
	if len(newName) > 48 {
		return fmt.Errorf("workspace name cannot be longer than 48 characters")
	}
	return nil
}

func validateWorkspaceExists(devsyConfig *config.Config, oldName string) error {
	wsConfig, err := provider.LoadWorkspaceConfig(devsyConfig.DefaultContext, oldName)
	if err != nil {
		return fmt.Errorf("workspace %s not found", oldName)
	}
	if wsConfig == nil {
		return fmt.Errorf("workspace %s not found", oldName)
	}

	return nil
}
