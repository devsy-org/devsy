package pro

import (
	"bytes"
	"context"
	"fmt"

	"github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	oldlog "github.com/devsy-org/log"
	"github.com/spf13/cobra"
)

// SelfCmd holds the cmd flags.
type SelfCmd struct {
	*flags.GlobalFlags

	Host string
}

// NewSelfCmd creates a new command.
func NewSelfCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &SelfCmd{
		GlobalFlags: globalFlags,
	}
	c := &cobra.Command{
		Use:    "self",
		Short:  "Get self",
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

	return c
}

func (cmd *SelfCmd) Run(
	ctx context.Context,
	devsyConfig *config.Config,
	provider *provider.ProviderConfig,
) error {
	var buf bytes.Buffer

	err := clientimplementation.RunCommandWithBinaries(clientimplementation.CommandOptions{
		Ctx:     ctx,
		Name:    "getSelf",
		Command: provider.Exec.Proxy.Get.Self,
		Context: devsyConfig.DefaultContext,
		Options: devsyConfig.ProviderOptions(provider.Name),
		Config:  provider,
		Stdout:  &buf,
		Log:     oldlog.Default,
	})
	if err != nil {
		return fmt.Errorf("get self: %w", err)
	}

	fmt.Println(buf.String())

	return nil
}
