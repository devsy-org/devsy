package workspace

import (
	"bytes"
	"context"
	"fmt"

	"github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/cmd/pro/proutil"
	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	"github.com/devsy-org/devsy/pkg/config"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/platform"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/spf13/cobra"
)

// UpdateWorkspaceCmd holds the cmd flags.
type UpdateWorkspaceCmd struct {
	*flags.GlobalFlags

	Host     string
	Instance string
}

// NewUpdateCmd creates a new command.
//
//nolint:dupl // structurally similar to NewCreateCmd; intentional sibling factory
func NewUpdateCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &UpdateWorkspaceCmd{
		GlobalFlags: globalFlags,
	}
	c := &cobra.Command{
		Use:    "update",
		Short:  "Update workspace instance",
		Hidden: true,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			devsyConfig, provider, err := proutil.FindProProvider(
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

	cliflags.Add(c, cliflags.String(&cmd.Host, names.Host, "", "The pro instance to use"))
	_ = c.MarkFlagRequired(names.Host)
	flags.BindEnv(c.Flags(), names.Host)
	cliflags.Add(
		c,
		cliflags.String(&cmd.Instance, names.Instance, "", "The workspace instance to update"),
	)
	_ = c.MarkFlagRequired(names.Instance)

	return c
}

func (cmd *UpdateWorkspaceCmd) Run(
	ctx context.Context,
	devsyConfig *config.Config,
	provider *provider.ProviderConfig,
) error {
	opts := devsyConfig.ProviderOptions(provider.Name)
	opts[platform.WorkspaceInstanceEnv] = config.OptionValue{Value: cmd.Instance}

	var buf bytes.Buffer

	err := clientimplementation.RunCommandWithBinaries(clientimplementation.CommandOptions{
		Ctx:     ctx,
		Command: provider.Exec.Proxy.Update.Workspace,
		Context: devsyConfig.DefaultContext,
		Options: opts,
		Config:  provider,
		Stdout:  &buf,
		Stderr:  log.Writer(log.LevelError),
	})
	if err != nil {
		return fmt.Errorf("update workspace with provider %q: %w", provider.Name, err)
	}

	fmt.Println(buf.String())

	return nil
}
