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

// NewPlatformCmd returns a new command.
func NewPlatformCmd(flags *flags.GlobalFlags) *cobra.Command {
	globalFlags := &proflags.GlobalFlags{GlobalFlags: flags}
	platformCmd := &cobra.Command{
		Use:           "platform",
		Aliases:       []string{"pro"},
		Short:         "Devsy Platform commands",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PersistentPreRunE: func(c *cobra.Command, _ []string) error {
			globalFlags = proflags.SetGlobalFlags(c.PersistentFlags())
			return nil
		},
	}

	platformCmd.AddCommand(NewLoginCmd(globalFlags))
	platformCmd.AddCommand(NewLogoutCmd(globalFlags))
	platformCmd.AddCommand(NewListCmd(globalFlags))
	platformCmd.AddCommand(NewStartCmd(globalFlags))
	platformCmd.AddCommand(NewSelfCmd(globalFlags))
	platformCmd.AddCommand(NewVersionCmd(globalFlags))
	platformCmd.AddCommand(NewHealthCmd(globalFlags))
	platformCmd.AddCommand(NewCheckUpdateCmd(globalFlags))
	platformCmd.AddCommand(NewUpdateProviderCmd(globalFlags))

	platformCmd.AddCommand(procluster.NewClusterCmd(globalFlags))
	platformCmd.AddCommand(proproject.NewProjectCmd(globalFlags))
	platformCmd.AddCommand(protemplate.NewTemplateCmd(globalFlags))
	platformCmd.AddCommand(prouser.NewUserCmd(globalFlags))
	platformCmd.AddCommand(proworkspace.NewWorkspaceCmd(globalFlags))
	platformCmd.AddCommand(daemon.NewCmd(globalFlags))
	platformCmd.AddCommand(provider.NewPlatformProviderCmd(globalFlags))
	return platformCmd
}
