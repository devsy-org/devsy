package ide

import (
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/ide/ideparse"
	"github.com/spf13/cobra"
)

// NewIDECmd returns a new command.
func NewIDECmd(flags *flags.GlobalFlags) *cobra.Command {
	ideCmd := &cobra.Command{
		Use:   "ide",
		Short: "Devsy IDE commands",
	}

	ideCmd.AddCommand(NewUseCmd(flags))
	ideCmd.AddCommand(NewSetCmd(flags))
	ideCmd.AddCommand(NewOptionsCmd(flags))
	ideCmd.AddCommand(NewListCmd(flags))
	return ideCmd
}

func ideNameCompletion(
	_ *cobra.Command,
	args []string,
	_ string,
) ([]string, cobra.ShellCompDirective) {
	if len(args) != 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	names := make([]string, 0, len(ideparse.AllowedIDEs))
	for _, entry := range ideparse.AllowedIDEs {
		names = append(names, string(entry.Name))
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}
