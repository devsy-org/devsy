package pro

import (
	"github.com/devsy-org/devsy/cmd/flags"
	procluster "github.com/devsy-org/devsy/cmd/pro/cluster"
	"github.com/devsy-org/devsy/cmd/pro/daemon"
	proflags "github.com/devsy-org/devsy/cmd/pro/flags"
	proproject "github.com/devsy-org/devsy/cmd/pro/project"
	"github.com/devsy-org/devsy/cmd/pro/provider"
	protemplate "github.com/devsy-org/devsy/cmd/pro/template"
	prouser "github.com/devsy-org/devsy/cmd/pro/user"
	proworkspace "github.com/devsy-org/devsy/cmd/pro/workspace"
	"github.com/spf13/cobra"
)

// NewProCmd returns a new command.
func NewProCmd(flags *flags.GlobalFlags) *cobra.Command {
	globalFlags := &proflags.GlobalFlags{GlobalFlags: flags}
	proCmd := &cobra.Command{
		Use:           "pro",
		Short:         "Devsy Pro commands",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PersistentPreRunE: func(c *cobra.Command, _ []string) error {
			globalFlags = proflags.SetGlobalFlags(c.PersistentFlags())
			return nil
		},
	}

	proCmd.AddCommand(NewLoginCmd(globalFlags))
	proCmd.AddCommand(NewLogoutCmd(globalFlags))
	proCmd.AddCommand(NewListCmd(globalFlags))
	proCmd.AddCommand(NewStartCmd(globalFlags))
	proCmd.AddCommand(NewSelfCmd(globalFlags))
	proCmd.AddCommand(NewVersionCmd(globalFlags))
	proCmd.AddCommand(NewHealthCmd(globalFlags))
	proCmd.AddCommand(NewCheckUpdateCmd(globalFlags))
	proCmd.AddCommand(NewUpdateProviderCmd(globalFlags))

	proCmd.AddCommand(procluster.NewClusterCmd(globalFlags))
	proCmd.AddCommand(proproject.NewProjectCmd(globalFlags))
	proCmd.AddCommand(protemplate.NewTemplateCmd(globalFlags))
	proCmd.AddCommand(prouser.NewUserCmd(globalFlags))
	proCmd.AddCommand(proworkspace.NewWorkspaceCmd(globalFlags))
	proCmd.AddCommand(daemon.NewCmd(globalFlags))
	proCmd.AddCommand(provider.NewProProviderCmd(globalFlags))
	return proCmd
}
