package pro

import (
	"bytes"
	"context"
	"fmt"

	"github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/platform"
	"github.com/devsy-org/devsy/pkg/provider"
	devsylog "github.com/devsy-org/devsy/pkg/log"
	"github.com/spf13/cobra"
)

// CreateWorkspaceCmd holds the cmd flags.
type CreateWorkspaceCmd struct {
	*flags.GlobalFlags

	Host     string
	Instance string
}

// NewCreateWorkspaceCmd creates a new command.
func NewCreateWorkspaceCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &CreateWorkspaceCmd{
		GlobalFlags: globalFlags,
	}
	c := &cobra.Command{
		Use:    "create-workspace",
		Short:  "Create workspace instance",
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
	c.Flags().StringVar(&cmd.Instance, "instance", "", "The workspace instance to create")
	_ = c.MarkFlagRequired("instance")

	return c
}

func (cmd *CreateWorkspaceCmd) Run(
	ctx context.Context,
	devsyConfig *config.Config,
	provider *provider.ProviderConfig,
) error {
	opts := devsyConfig.ProviderOptions(provider.Name)
	opts[platform.WorkspaceInstanceEnv] = config.OptionValue{Value: cmd.Instance}

	var buf bytes.Buffer

	err := clientimplementation.RunCommandWithBinaries(clientimplementation.CommandOptions{
		Ctx:     ctx,
		Name:    "createWorkspace",
		Command: provider.Exec.Proxy.Create.Workspace,
		Context: devsyConfig.DefaultContext,
		Options: opts,
		Config:  provider,
		Stdout:  &buf,
		Stderr:  devsylog.Writer(devsylog.LevelError),
	})
	if err != nil {
		return fmt.Errorf("create workspace: %w", err)
	}

	fmt.Println(buf.String())

	return nil
}
