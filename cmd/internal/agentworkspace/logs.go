package agentworkspace

import (
	"context"
	"fmt"
	"os"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/agent"
	"github.com/devsy-org/devsy/pkg/config"
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
	// `agent workspace logs` fetches the devcontainer's logs from the docker
	// daemon (docker logs). It runs wherever that daemon and the per-workspace
	// state live — inside the devcontainer, or on the machine/host that hosts
	// it — so it is NOT gated on running inside a container. Missing workspace
	// state (e.g. the user's own CLI with no such workspace) surfaces below.
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

	// create new runner
	runner, err := devcontainer.NewRunner(
		config.ContainerDevsyHelperLocation,
		config.DefaultAgentDownloadURL(),
		workspaceInfo,
	)
	if err != nil {
		return fmt.Errorf("create runner: %w", err)
	}

	// write devcontainer logs to stdout
	return runner.Logs(ctx, os.Stdout)
}
