package machine

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/workspace"
	oldlog "github.com/devsy-org/log"
	"github.com/spf13/cobra"
)

type InspectCmd struct {
	*flags.GlobalFlags
}

func NewInspectCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &InspectCmd{
		GlobalFlags: flags,
	}
	stopCmd := &cobra.Command{
		Use:   "inspect",
		Short: "Inspects an existing machine",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args)
		},
	}

	return stopCmd
}

func (cmd *InspectCmd) Run(ctx context.Context, args []string) error {
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}

	machineClient, err := workspace.GetMachine(devsyConfig, args, oldlog.Default)
	if err != nil {
		return err
	}
	p, err := provider.LoadProviderConfig(devsyConfig.DefaultContext, machineClient.Provider())
	if err != nil {
		return err
	}

	machineConfig := machineClient.MachineConfig()
	for k := range machineConfig.Provider.Options {
		optConfig := p.Options[k]
		if optConfig.Hidden {
			delete(machineConfig.Provider.Options, k)
			continue
		}

		if optConfig.Password {
			opt := machineConfig.Provider.Options[k]
			opt.Value = "********"
		}
	}

	out, err := json.MarshalIndent(machineConfig, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(out))

	return nil
}
