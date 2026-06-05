package machine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// StatusCmd holds the configuration.
type StatusCmd struct {
	*flags.GlobalFlags
}

// NewStatusCmd creates a new status command.
func NewStatusCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &StatusCmd{
		GlobalFlags: flags,
	}
	statusCmd := &cobra.Command{
		Use:   "status [name]",
		Short: "Retrieves the status of an existing machine",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args)
		},
	}

	return statusCmd
}

// Run runs the command logic.
func (cmd *StatusCmd) Run(ctx context.Context, args []string) error {
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}

	machineClient, err := workspace.GetMachine(devsyConfig, args)
	if err != nil {
		return err
	}

	// get status
	machineStatus, err := machineClient.Status(ctx, client.StatusOptions{})
	if err != nil {
		return err
	}

	mode, err := output.ResolveMode(cmd.ResultFormat)
	if err != nil {
		return err
	}
	switch mode {
	case output.ModePlain:
		switch machineStatus {
		case client.StatusStopped:
			_, _ = fmt.Fprintf(
				os.Stdout,
				"Machine %q is %q, you can start it via 'devsy machine start %s'\n",
				machineClient.Machine(),
				machineStatus,
				machineClient.Machine(),
			)
		case client.StatusBusy:
			_, _ = fmt.Fprintf(
				os.Stdout,
				"Machine %q is %q, which means its currently unaccessible. "+
					"This is usually resolved by waiting a couple of minutes\n",
				machineClient.Machine(),
				machineStatus,
			)
		case client.StatusNotFound:
			_, _ = fmt.Fprintf(
				os.Stdout,
				"Machine %q is %q\n",
				machineClient.Machine(),
				machineStatus,
			)
		default:
			_, _ = fmt.Fprintf(
				os.Stdout,
				"Machine %q is %q\n",
				machineClient.Machine(),
				machineStatus,
			)
		}
	case output.ModeJSON:
		out, err := json.Marshal(struct {
			ID       string `json:"id,omitempty"`
			Context  string `json:"context,omitempty"`
			Provider string `json:"provider,omitempty"`
			State    string `json:"state,omitempty"`
		}{
			ID:       machineClient.Machine(),
			Context:  machineClient.Context(),
			Provider: machineClient.Provider(),
			State:    string(machineStatus),
		})
		if err != nil {
			return err
		}

		fmt.Print(string(out))
	}

	return nil
}
