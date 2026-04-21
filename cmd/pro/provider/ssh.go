package provider

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/platform"
	"github.com/devsy-org/devsy/pkg/platform/client"
	"github.com/devsy-org/devsy/pkg/platform/remotecommand"
	"github.com/spf13/cobra"
)

// SshCmd holds the cmd flags.
type SshCmd struct {
	*flags.GlobalFlags
}

// NewSshCmd creates a new command.
func NewSshCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &SshCmd{
		GlobalFlags: globalFlags,
	}
	c := &cobra.Command{
		Hidden: true,
		Use:    "ssh",
		Short:  "Runs ssh on a workspace",
		Args:   cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), os.Stdin, os.Stdout, os.Stderr)
		},
	}

	return c
}

func (cmd *SshCmd) Run(
	ctx context.Context,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) error {
	baseClient, err := client.InitClientFromPath(ctx, cmd.Config)
	if err != nil {
		return err
	}

	info, err := platform.GetWorkspaceInfoFromEnv()
	if err != nil {
		return err
	}
	opts := platform.FindInstanceOptions{UID: info.UID, ProjectName: info.ProjectName}
	workspace, err := platform.FindInstance(ctx, baseClient, opts)
	if err != nil {
		return err
	} else if workspace == nil {
		return fmt.Errorf("couldn't find workspace")
	}

	conn, err := platform.DialInstance(
		baseClient,
		workspace,
		"ssh",
		platform.OptionsFromEnv(config.EnvFlagsSSH),
	)
	if err != nil {
		return err
	}

	_, err = remotecommand.ExecuteConn(
		ctx,
		conn,
		stdin,
		stdout,
		stderr,
	)
	if err != nil {
		return fmt.Errorf("error executing: %w", err)
	}

	return nil
}
