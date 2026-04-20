package get

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/pkg/platform"
	"github.com/devsy-org/devsy/pkg/platform/client"
	"github.com/spf13/cobra"
)

// WorkspaceCmd holds the cmd flags.
type WorkspaceCmd struct {
	*flags.GlobalFlags
}

// NewWorkspaceCmd creates a new command.
func NewWorkspaceCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &WorkspaceCmd{
		GlobalFlags: globalFlags,
	}
	c := &cobra.Command{
		Use:   "workspace",
		Short: "Get workspace for the provider",
		Args:  cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}

	return c
}

func (cmd *WorkspaceCmd) Run(ctx context.Context) error {
	baseClient, err := client.InitClientFromPath(ctx, cmd.Config)
	if err != nil {
		return err
	}

	workspaceInfo, err := platform.GetWorkspaceInfoFromEnv()
	if err != nil {
		return err
	}

	opts := platform.FindInstanceOptions{
		UID:         workspaceInfo.UID,
		ProjectName: workspaceInfo.ProjectName,
	}
	instance, err := platform.FindInstance(ctx, baseClient, opts)
	if err != nil {
		return err
	}

	instanceBytes, err := json.Marshal(instance)
	if err != nil {
		return nil
	}

	fmt.Println(string(instanceBytes))

	return nil
}
