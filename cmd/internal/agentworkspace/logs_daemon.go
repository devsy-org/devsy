package agentworkspace

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"github.com/devsy-org/devsy/cmd/flags"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/machinediagnostics"
	"github.com/spf13/cobra"
)

// LogsDaemonCmd holds the cmd flags.
type LogsDaemonCmd struct {
	*flags.GlobalFlags

	ID string
}

// NewLogsDaemonCmd creates a new command.
func NewLogsDaemonCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &LogsDaemonCmd{
		GlobalFlags: flags,
	}
	logsDaemonCmd := &cobra.Command{
		Use:   "logs-daemon",
		Short: "Returns the daemon logs",
		Args:  cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, _ []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}
	cliflags.Add(logsDaemonCmd, cliflags.String(&cmd.ID, names.ID, "", "The workspace id"))
	_ = logsDaemonCmd.MarkFlagRequired(names.ID)
	return logsDaemonCmd
}

func (cmd *LogsDaemonCmd) Run(ctx context.Context) error {
	_ = ctx
	response := machinediagnostics.ReadFromLocator(
		machinediagnostics.DefaultLocatorPath,
		"",
		machinediagnostics.DefaultReadEvents,
		0,
		time.Now(),
	)
	return json.NewEncoder(os.Stdout).Encode(response)
}
