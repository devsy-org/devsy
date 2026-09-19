package machine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/machinediagnostics"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

type LogsCmd struct {
	*flags.GlobalFlags
	After  string
	Limit  int
	Follow bool
}

func NewLogsCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &LogsCmd{GlobalFlags: globalFlags}
	c := &cobra.Command{
		Use:   "logs [name]",
		Short: "Show Devsy machine diagnostic events",
		RunE:  func(c *cobra.Command, args []string) error { return cmd.Run(c.Context(), args) },
	}
	cliflags.Add(
		c,
		cliflags.String(&cmd.After, "after", "", "An opaque diagnostics cursor"),
		cliflags.Int(
			&cmd.Limit,
			"limit",
			machinediagnostics.DefaultReadEvents,
			"Maximum diagnostic events",
		),
	)
	c.Flags().
		BoolVarP(&cmd.Follow, "follow", "f", false, "Poll for new diagnostic events until interrupted")
	return c
}

//nolint:cyclop // follow mode owns a single ordered polling loop.
func (cmd *LogsCmd) Run(
	ctx context.Context,
	args []string,
) error {
	if cmd.Limit < 0 || cmd.Limit > machinediagnostics.MaxReadEvents {
		return fmt.Errorf(
			"diagnostics limit must be between 0 and %d",
			machinediagnostics.MaxReadEvents,
		)
	}
	if err := machinediagnostics.ValidateCursor(cmd.After); err != nil {
		return fmt.Errorf("invalid diagnostics cursor: %w", err)
	}
	cfg, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}
	mc, err := workspace.GetMachine(cfg, args)
	if err != nil {
		return err
	}
	mode, err := output.ResolveMode(cmd.ResultFormat)
	if err != nil {
		return err
	}
	for {
		result, err := fetchDiagnostics(ctx, mc, cmd.After, cmd.Limit, true)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if mode == output.ModeJSON {
			if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
				return err
			}
		} else {
			if result.Source.Availability != string(machinediagnostics.AvailabilityAvailable) {
				_, _ = fmt.Fprintf(
					os.Stderr,
					"Diagnostics: %s %s\n",
					result.Source.Availability,
					result.Source.Message,
				)
			}
			if result.Cursor.State == machinediagnostics.CursorGap ||
				result.Cursor.State == machinediagnostics.CursorReset {
				_, _ = fmt.Fprintf(
					os.Stderr,
					"Diagnostic history: %s (%s)\n",
					result.Cursor.State,
					result.Cursor.Reason,
				)
			}
			for _, event := range result.Events {
				_, _ = fmt.Fprintf(
					os.Stdout,
					"%s %-5s %-35s %s\n",
					event.Timestamp.Format(time.RFC3339),
					event.Level,
					event.Type,
					event.Message,
				)
			}
		}
		if !cmd.Follow {
			return nil
		}
		if result.Cursor.State == machinediagnostics.CursorReset {
			cmd.After = ""
		}
		if result.Cursor.Next != "" {
			cmd.After = result.Cursor.Next
		}
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}
