package pro

import (
	"bytes"
	"context"
	"fmt"

	"github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/spf13/cobra"
)

// VersionCmd holds the cmd flags.
type VersionCmd struct {
	*flags.GlobalFlags

	Host string
}

// NewVersionCmd creates a new command.
func NewVersionCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &VersionCmd{
		GlobalFlags: globalFlags,
	}
	c := &cobra.Command{
		Use:    "version",
		Short:  "Get version",
		Hidden: true,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			devsyConfig, provider, err := findProProvider(
				cobraCmd.Context(),
				cmd.Context,
				cmd.Provider,
				cmd.Host,
			)
			if err != nil {
				return err
			}

			return cmd.Run(cobraCmd.Context(), devsyConfig, provider)
		},
	}

	c.Flags().StringVar(&cmd.Host, "host", "", "The pro instance to use")
	_ = c.MarkFlagRequired("host")
	flags.BindEnv(c.Flags(), "host")

	return c
}

func (cmd *VersionCmd) Run(
	ctx context.Context,
	devsyConfig *config.Config,
	providerConfig *provider.ProviderConfig,
) error {
	opts := devsyConfig.ProviderOptions(providerConfig.Name)
	opts[config.EnvProviderID] = config.OptionValue{Value: providerConfig.Name}
	opts[config.EnvProviderContext] = config.OptionValue{Value: cmd.Context}

	var buf bytes.Buffer

	err := clientimplementation.RunCommandWithBinaries(clientimplementation.CommandOptions{
		Ctx:     ctx,
		Name:    "getVersion",
		Command: providerConfig.Exec.Proxy.Get.Version,
		Context: devsyConfig.DefaultContext,
		Options: opts,
		Config:  providerConfig,
		Stdout:  &buf,
	})
	if err != nil {
		return fmt.Errorf("get version: %w", err)
	}

	fmt.Print(buf.String())

	return nil
}
