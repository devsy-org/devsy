package agentworkspace

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/agent"
	"github.com/spf13/cobra"
)

// LogsDaemonCmd holds the cmd flags.
type LogsDaemonCmd struct {
	*flags.GlobalFlags

	ID string
}

// NewLogsDaemonCmd creates a new command.
func NewLogsDaemonCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &LogsDaemonCmd{
		GlobalFlags: flags,
	}
	logsDaemonCmd := &cobra.Command{
		Use:   "logs-daemon",
		Short: "Returns the daemon logs",
		Args:  cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, _ []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}
	logsDaemonCmd.Flags().StringVar(&cmd.ID, "id", "", "The workspace id")
	_ = logsDaemonCmd.MarkFlagRequired("id")
	return logsDaemonCmd
}

func (cmd *LogsDaemonCmd) Run(ctx context.Context) error {
	// `agent workspace logs-daemon` reads agent-daemon.log, which only
	// exists inside the workspace container or machine. Reject host
	// invocations explicitly to surface misconfigurations early.
	if agent.IsHostAgentInvocation(cmd.AgentDir) {
		return fmt.Errorf(
			"`devsy internal agent workspace logs-daemon` is only valid inside the workspace container or machine",
		)
	}

	// get workspace
	shouldExit, _, err := agent.ReadAgentWorkspaceInfo(
		cmd.AgentDir,
		cmd.Context,
		cmd.ID,
	)
	if err != nil {
		return err
	} else if shouldExit {
		return nil
	}

	logDir, err := agent.GetAgentDaemonLogDir(cmd.AgentDir)
	if err != nil {
		return err
	}

	// #nosec G304 -- reads the agent's own daemon log at a derived path.
	f, err := os.Open(filepath.Join(logDir, "agent-daemon.log"))
	if err != nil {
		return fmt.Errorf("open agent-daemon.log: %w", err)
	}
	defer func() { _ = f.Close() }()

	_, err = io.Copy(os.Stdout, f)
	return err
}
