package container

import (
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/envfile"
	"github.com/spf13/cobra"
)

// NewContainerCmd returns the container command. Cobra runs only the nearest
// persistent pre-run hook, so the root hook is chained explicitly to keep
// log-level validation and context-deletion recovery in effect.
func NewContainerCmd(
	globalFlags *flags.GlobalFlags,
	rootPreRunE func(*cobra.Command, []string) error,
) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "container",
		Short: "Inspect the current container",
		PersistentPreRunE: func(command *cobra.Command, args []string) error {
			envfile.Apply()
			if rootPreRunE != nil {
				return rootPreRunE(command, args)
			}
			return nil
		},
	}
	cmd.AddCommand(newInfoCmd(globalFlags))
	return cmd
}
