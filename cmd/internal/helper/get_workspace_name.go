package helper

import (
	"context"
	"fmt"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/file"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

type GetWorkspaceNameCommand struct {
	*flags.GlobalFlags
}

// NewGetWorkspaceNameCmd creates a new command.
func NewGetWorkspaceNameCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &GetWorkspaceNameCommand{
		GlobalFlags: flags,
	}
	shellCmd := &cobra.Command{
		Use:   "get-workspace-name",
		Short: "Retrieves a workspace name",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args)
		},
	}

	return shellCmd
}

func (cmd *GetWorkspaceNameCommand) Run(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("workspace is missing")
	}

	_, name := file.IsLocalDir(args[0])
	workspaceID := workspace.ToID(name)
	fmt.Print(workspaceID)
	return nil
}
