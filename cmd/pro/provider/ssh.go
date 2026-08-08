//nolint:dupl // structurally similar to stop.go; intentional sibling command sharing dialAndExecute
package provider

import (
	"context"
	"io"
	"os"

	"github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/platform"
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
	return dialAndExecute(ctx, dialAndExecuteParams{
		configPath: cmd.Config,
		action:     "ssh",
		envFlags:   platform.OptionsFromEnv(config.EnvFlagsSSH),
		stdin:      stdin,
		stdout:     stdout,
		stderr:     stderr,
	})
}
