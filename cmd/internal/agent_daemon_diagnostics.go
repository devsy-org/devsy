package cmdinternal

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/devsy-org/devsy/cmd/flags"
	agentdaemon "github.com/devsy-org/devsy/pkg/daemon/agent"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/machinediagnostics"
	"github.com/spf13/cobra"
)

// DaemonDiagnosticsCmd is the machine-side, JSON-only diagnostics reader.
type DaemonDiagnosticsCmd struct {
	*flags.GlobalFlags
	After       string
	Limit       int
	StateRoot   string
	StateLayout string
}

func NewDaemonDiagnosticsCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &DaemonDiagnosticsCmd{GlobalFlags: globalFlags}
	cobraCmd := &cobra.Command{
		Use:    "daemon-diagnostics",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE:   func(c *cobra.Command, _ []string) error { return cmd.Run(c.Context()) },
	}
	cliflags.Add(
		cobraCmd,
		cliflags.String(&cmd.After, "after", "", "An opaque diagnostics cursor"),
		cliflags.Int(
			&cmd.Limit,
			"limit",
			machinediagnostics.DefaultReadEvents,
			"Maximum diagnostic events",
		),
		cliflags.String(&cmd.StateRoot, names.StateRoot, "", "Diagnostics state-root override"),
		cliflags.String(
			&cmd.StateLayout,
			names.StateLayout,
			"",
			"Diagnostics state-layout override",
		),
	)
	_ = cobraCmd.Flags().MarkHidden(names.StateRoot)
	_ = cobraCmd.Flags().MarkHidden(names.StateLayout)
	return cobraCmd
}

func (cmd *DaemonDiagnosticsCmd) Run(
	_ context.Context,
) error {
	if cmd.Limit < 0 || cmd.Limit > machinediagnostics.MaxReadEvents {
		return fmt.Errorf(
			"diagnostics limit must be between 0 and %d",
			machinediagnostics.MaxReadEvents,
		)
	}
	response, err := cmd.readResponse()
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(response)
}

func (cmd *DaemonDiagnosticsCmd) readResponse() (machinediagnostics.ReadResponse, error) {
	options := machinediagnostics.ReadOptions{
		After:    cmd.After,
		Limit:    cmd.Limit,
		Interval: time.Minute,
		Now:      time.Now(),
	}
	if cmd.StateRoot == "" && cmd.StateLayout == "" {
		return machinediagnostics.ReadFromLocator(
			machinediagnostics.DefaultLocatorPath,
			options,
		), nil
	}
	if cmd.StateRoot == "" || cmd.StateLayout == "" {
		return machinediagnostics.ReadResponse{}, fmt.Errorf(
			"daemon state root and state layout must be provided together",
		)
	}
	location := agentdaemon.StateLocation{
		Root:   cmd.StateRoot,
		Layout: agentdaemon.StateLayout(cmd.StateLayout),
	}
	if err := location.Validate(); err != nil {
		return machinediagnostics.ReadResponse{}, err
	}
	return machinediagnostics.Read(machinediagnostics.DiagnosticsDir(location.Root), options), nil
}
