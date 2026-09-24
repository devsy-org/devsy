package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/devsy-org/devsy/cmd/completion"
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/table"
	workspace2 "github.com/devsy-org/devsy/pkg/workspace"
	"github.com/devsy-org/devsy/pkg/workspacejournal"
	"github.com/spf13/cobra"
)

type EventsCmd struct {
	*flags.GlobalFlags
	Limit int
}

func NewEventsCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &EventsCmd{GlobalFlags: globalFlags, Limit: workspacejournal.DefaultLimit}
	eventsCmd := &cobra.Command{
		Use:   "events [flags] [workspace-path|workspace-name]",
		Short: "Show workspace operation events",
		RunE:  func(c *cobra.Command, args []string) error { return cmd.execute(c.Context(), args) },
		ValidArgsFunction: func(root *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			return completion.GetWorkspaceSuggestions(
				root,
				cmd.Context,
				cmd.Provider,
				args,
				toComplete,
				cmd.Owner,
			)
		},
	}
	cliflags.Add(
		eventsCmd,
		cliflags.Int(
			&cmd.Limit,
			"limit",
			workspacejournal.DefaultLimit,
			"Maximum number of recent events",
		),
	)
	return eventsCmd
}

func (cmd *EventsCmd) execute(ctx context.Context, args []string) error {
	cfg, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}
	client, err := workspace2.Get(
		ctx,
		workspace2.GetOptions{DevsyConfig: cfg, Args: args, Owner: cmd.Owner},
	)
	if err != nil {
		return err
	}
	dir, err := workspacejournal.DefaultDir()
	if err != nil {
		return err
	}
	events, err := workspacejournal.Read(dir, client.Workspace(), cmd.Limit)
	if err != nil {
		return err
	}
	return cmd.print(events)
}

func (cmd *EventsCmd) print(events []workspacejournal.Event) error {
	mode, err := output.ResolveMode(cmd.ResultFormat)
	if err != nil {
		return err
	}
	if mode == output.ModeJSON {
		return json.NewEncoder(os.Stdout).Encode(struct {
			SchemaVersion int                      `json:"schemaVersion"`
			Events        []workspacejournal.Event `json:"events"`
		}{workspacejournal.SchemaVersion, events})
	}
	printEvents(events)
	return nil
}

func printEvents(events []workspacejournal.Event) {
	rows := make([][]string, 0, len(events))
	for _, event := range events {
		message := ""
		if event.Error != nil {
			message = event.Error.Message
		}
		rows = append(rows, []string{
			event.Timestamp.Format("2006-01-02 15:04:05Z07:00"),
			string(event.Phase),
			string(event.State),
			timeDuration(event.DurationMillis),
			message,
		})
	}
	table.Print([]string{"Time", "Phase", "State", "Duration", "Error"}, rows)
}

func timeDuration(milliseconds int64) string {
	if milliseconds == 0 {
		return "-"
	}
	return fmt.Sprintf("%dms", milliseconds)
}
