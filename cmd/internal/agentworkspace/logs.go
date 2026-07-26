package agentworkspace

import (
	"context"
	"fmt"
	"os"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/agent"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/spf13/cobra"
)

// LogsCmd holds the cmd flags.
type LogsCmd struct {
	*flags.GlobalFlags

	ID string
}

// NewLogsCmd creates a new command.
func NewLogsCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &LogsCmd{
		GlobalFlags: flags,
	}
	c := &cobra.Command{
		Use:   "logs",
		Short: "Returns the workspace container logs",
		Args:  cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, _ []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}
	cliflags.Add(c, cliflags.String(&cmd.ID, names.ID, "", "The workspace id"))
	_ = c.MarkFlagRequired(names.ID)

	return c
}

func (cmd *LogsCmd) Run(ctx context.Context) error {
	shouldExit, workspaceInfo, err := agent.ReadAgentWorkspaceInfo(
		cmd.AgentDir,
		cmd.Context,
		cmd.ID,
	)
	if err != nil {
		return err
	} else if shouldExit {
		return nil
	}
	if workspaceInfo == nil {
		return fmt.Errorf("workspace %q not found", cmd.ID)
	}

	runner, err := devcontainer.NewRunner(
		ctx,
		config.ContainerDevsyHelperLocation,
		config.DefaultAgentDownloadURL(),
		workspaceInfo,
	)
	if err != nil {
		return fmt.Errorf("create runner: %w", err)
	}

	return runner.Logs(ctx, os.Stdout)
}
