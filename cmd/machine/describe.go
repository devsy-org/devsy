package machine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// DescribeCmd holds the configuration.
type DescribeCmd struct {
	*flags.GlobalFlags
}

// NewDescribeCmd creates a new describe command.
func NewDescribeCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &DescribeCmd{
		GlobalFlags: flags,
	}
	describeCmd := &cobra.Command{
		Use:   "describe [name]",
		Short: "Show a machine's description",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args)
		},
	}

	return describeCmd
}

// Run runs the command logic.
func (cmd *DescribeCmd) Run(ctx context.Context, args []string) error {
	mode, err := output.ResolveMode(cmd.ResultFormat)
	if err != nil {
		return err
	}
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}

	machineClient, err := workspace.GetMachine(devsyConfig, args)
	if err != nil {
		return err
	}

	// get description
	machineDescription, err := machineClient.Describe(ctx)
	if err != nil {
		return err
	}
	if mode == output.ModeJSON {
		return json.NewEncoder(os.Stdout).Encode(struct {
			Description string `json:"description"`
		}{machineDescription})
	}

	_, _ = fmt.Fprintln(os.Stdout, machineDescription)

	return nil
}
