package provider

import (
	"context"
	"io"
	"os"

	storagev1 "github.com/devsy-org/api/pkg/apis/storage/v1"
	"github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/pkg/platform"
	"github.com/spf13/cobra"
)

// StopCmd holds the cmd flags.
type StopCmd struct {
	*flags.GlobalFlags
}

// NewStopCmd creates a new command.
func NewStopCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &StopCmd{
		GlobalFlags: globalFlags,
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
	return dialAndExecute(
		ctx,
		cmd.Config,
		"stop",
		platform.OptionsFromEnv(storagev1.DevsyFlagsStop),
		stdin,
		stdout,
		stderr,
	)
}
