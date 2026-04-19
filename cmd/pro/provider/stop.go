package provider

import (
	"context"
	"fmt"
	"io"
	"os"

	storagev1 "github.com/devsy-org/api/pkg/apis/storage/v1"
	"github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/pkg/platform"
	"github.com/devsy-org/devsy/pkg/platform/client"
	"github.com/devsy-org/devsy/pkg/platform/remotecommand"
	"github.com/devsy-org/log"
	"github.com/spf13/cobra"
)

// StopCmd holds the cmd flags.
type StopCmd struct {
	*flags.GlobalFlags

	Log log.Logger
}

// NewStopCmd creates a new command.
func NewStopCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &StopCmd{
		GlobalFlags: globalFlags,
		Log:         log.GetInstance(),
	}
	c := &cobra.Command{
		Hidden: true,
		Use:    "stop",
		Short:  "Runs stop on a workspace",
		Args:   cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), os.Stdin, os.Stdout, os.Stderr)
		},
	}

	return c
}

func (cmd *StopCmd) Run(
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
		"stop",
		platform.OptionsFromEnv(storagev1.DevsyFlagsStop),
		cmd.Log,
	)
	if err != nil {
		return err
	}

	_, err = remotecommand.ExecuteConn(ctx, conn, stdin, stdout, stderr, cmd.Log.ErrorStreamOnly())
	if err != nil {
		return fmt.Errorf("error executing: %w", err)
	}

	return nil
}
