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

//nolint:cyclop // CLI validation and locator selection are intentionally linear.
func (cmd *DaemonDiagnosticsCmd) Run(
	_ context.Context,
) error {
	if cmd.Limit < 0 || cmd.Limit > machinediagnostics.MaxReadEvents {
		return fmt.Errorf(
			"diagnostics limit must be between 0 and %d",
			machinediagnostics.MaxReadEvents,
		)
	}
	dir := ""
	if cmd.StateRoot != "" || cmd.StateLayout != "" {
		if cmd.StateRoot == "" || cmd.StateLayout == "" {
			return fmt.Errorf("daemon state root and state layout must be provided together")
		}
		location := agentdaemon.StateLocation{
			Root:   cmd.StateRoot,
			Layout: agentdaemon.StateLayout(cmd.StateLayout),
		}
		if err := location.Validate(); err != nil {
			return err
		}
		dir = machinediagnostics.DiagnosticsDir(location.Root)
	} else {
		response := machinediagnostics.ReadFromLocator(
			machinediagnostics.DefaultLocatorPath,
			cmd.After,
			cmd.Limit,
			time.Minute,
			time.Now(),
		)
		return json.NewEncoder(os.Stdout).Encode(response)
	}
	response := machinediagnostics.Read(dir, cmd.After, cmd.Limit, time.Minute, time.Now())
	if dir == "" {
		response.Availability = machinediagnostics.AvailabilityNotInitialized
	}
	return json.NewEncoder(os.Stdout).Encode(response)
}
