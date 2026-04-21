package provider

import (
	"context"
	"fmt"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// UpdateCmd holds the cmd flags.
type UpdateCmd struct {
	*flags.GlobalFlags

	Use     bool
	Options []string
}

// NewUpdateCmd creates a new command.
func NewUpdateCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &UpdateCmd{
		GlobalFlags: flags,
	}
	updateCmd := &cobra.Command{
		Use:   "update [name] [name, GitHub link, URL or path]",
		Short: "Updates a provider in Devsy",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			ctx := cobraCmd.Context()
			devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
			if err != nil {
				return err
			}

			return cmd.Run(ctx, devsyConfig, args)
		},
	}

	updateCmd.Flags().
		BoolVar(&cmd.Use, "use", true, "If enabled will automatically activate the provider")
	updateCmd.Flags().
		StringArrayVarP(&cmd.Options, "option", "o", []string{}, "Provider option in the form KEY=VALUE")
	return updateCmd
}

func (cmd *UpdateCmd) Run(ctx context.Context, devsyConfig *config.Config, args []string) error {
	if len(args) != 1 && len(args) != 2 {
		return fmt.Errorf("please specify either a local file, URL or Git repository. " +
			"E.g. devsy provider update my-provider " + config.ProviderPrefix + "gcloud")
	}

	providerSource := ""
	if len(args) == 2 {
		providerSource = args[1]
	}

	providerConfig, err := workspace.UpdateProvider(
		devsyConfig,
		args[0],
		providerSource,
	)
	if err != nil {
		return err
	}

	log.Infof("updated provider: providerName=%s", providerConfig.Name)
	if cmd.Use {
		err = ConfigureProvider(ctx, ProviderOptionsConfig{
			Provider:       providerConfig,
			Context:        devsyConfig.DefaultContext,
			UserOptions:    cmd.Options,
			Reconfigure:    false,
			SkipRequired:   false,
			SkipInit:       false,
			SkipSubOptions: false,
			SingleMachine:  nil,
		})
		if err != nil {
			log.Errorf(
				"Error configuring provider, please retry with 'devsy provider use %s --reconfigure'",
				providerConfig.Name,
			)
			return fmt.Errorf("configure provider: %w", err)
		}

		return nil
	}

	log.Infof("To use the provider, please run the following command:")
	log.Infof("devsy provider use %s", providerConfig.Name)
	return nil
}
