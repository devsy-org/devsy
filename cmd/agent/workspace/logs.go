package workspace

import (
	"context"
	"fmt"
	"os"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/agent"
	"github.com/devsy-org/devsy/pkg/devcontainer"
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
	c.Flags().StringVar(&cmd.ID, "id", "", "The workspace id")
	_ = c.MarkFlagRequired("id")

	return c
}

func (cmd *LogsCmd) Run(ctx context.Context) error {
	// get workspace info
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

	// create new runner
	runner, err := devcontainer.NewRunner(
		agent.ContainerDevsyHelperLocation,
		agent.DefaultAgentDownloadURL(),
		workspaceInfo,
	)
	if err != nil {
		return fmt.Errorf("create runner: %w", err)
	}

	// write devcontainer logs to stdout
	return runner.Logs(ctx, os.Stdout)
}
