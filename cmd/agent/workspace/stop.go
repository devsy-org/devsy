package workspace

import (
	"context"
	"fmt"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/agent"
	"github.com/devsy-org/devsy/pkg/log"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	oldlog "github.com/devsy-org/log"
	"github.com/spf13/cobra"
)

// StopCmd holds the cmd flags.
type StopCmd struct {
	*flags.GlobalFlags

	WorkspaceInfo string
}

// NewStopCmd creates a new command.
func NewStopCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &StopCmd{
		GlobalFlags: flags,
	}
	stopCmd := &cobra.Command{
		Use:   "stop",
		Short: "Stops a workspace on the remote server",
		Args:  cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, _ []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}
	stopCmd.Flags().StringVar(&cmd.WorkspaceInfo, "workspace-info", "", "The workspace info")
	_ = stopCmd.MarkFlagRequired("workspace-info")
	return stopCmd
}

func (cmd *StopCmd) Run(ctx context.Context) error {
	logger := oldlog.Default.ErrorStreamOnly()

	// get workspace
	shouldExit, workspaceInfo, err := agent.WriteWorkspaceInfo(
		cmd.WorkspaceInfo,
		logger,
	)
	if err != nil {
		return fmt.Errorf("error parsing workspace info: %w", err)
	} else if shouldExit {
		return nil
	}

	// stop docker container
	err = stopContainer(ctx, workspaceInfo, logger)
	if err != nil {
		return fmt.Errorf("stop container: %w", err)
	}

	return nil
}

func stopContainer(
	ctx context.Context,
	workspaceInfo *provider2.AgentWorkspaceInfo,
	logger oldlog.Logger,
) error {
	log.Debugf("stopping Devsy container")
	runner, err := CreateRunner(workspaceInfo, logger)
	if err != nil {
		return err
	}

	err = runner.Stop(ctx)
	if err != nil {
		return err
	}
	log.Debugf("stopped Devsy container")

	return nil
}
