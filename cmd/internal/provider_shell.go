package cmdinternal

import (
	"context"
	"errors"
	"os"

	"github.com/devsy-org/devsy/pkg/shell"
	"github.com/spf13/cobra"
	"mvdan.cc/sh/v3/interp"
)

func NewProviderShellCmd() *cobra.Command {
	var scriptPath, forcePath, ackPath string
	var parentPID int
	command := &cobra.Command{
		Use: "provider-shell", Hidden: true, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cleanup, err := shell.PrepareProviderShellForeground(parentPID)
			if err != nil {
				return err
			}
			defer cleanup()
			err = shell.RunProviderShell(cmd.Context(), shell.ProviderShellInvocation{
				ScriptPath: scriptPath,
				ForcePath:  forcePath,
				AckPath:    ackPath,
				ParentPID:  parentPID,
			})
			if status, ok := errors.AsType[interp.ExitStatus](err); ok {
				os.Exit(int(status))
			}
			if errors.Is(err, context.Canceled) {
				os.Exit(1)
			}
			return err
		},
	}
	command.Flags().StringVar(&scriptPath, "script-file", "", "Private provider script file")
	command.Flags().StringVar(&forcePath, "force-file", "", "Private provider force marker")
	command.Flags().StringVar(&ackPath, "ack-file", "", "Private provider acknowledgement marker")
	command.Flags().IntVar(&parentPID, "parent-pid", 0, "Provider parent process ID")
	_ = command.Flags().MarkHidden("parent-pid")
	_ = command.MarkFlagRequired("parent-pid")
	_ = command.MarkFlagRequired("script-file")
	_ = command.MarkFlagRequired("force-file")
	_ = command.MarkFlagRequired("ack-file")
	return command
}
