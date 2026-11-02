package cmdinternal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"al.essio.dev/pkg/shellescape"
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/machinediagnostics"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// LogsDaemonCmd holds the configuration.
type LogsDaemonCmd struct {
	*flags.GlobalFlags
}

// NewLogsDaemonCmd creates a new destroy command.
func NewLogsDaemonCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &LogsDaemonCmd{
		GlobalFlags: flags,
	}
	startCmd := &cobra.Command{
		Use:   "logs-daemon",
		Short: "Prints the daemon logs on the machine",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args)
		},
	}

	return startCmd
}

// Run runs the command logic.
func (cmd *LogsDaemonCmd) Run(ctx context.Context, args []string) error {
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}

	workspaceClient, err := cmd.machineWorkspaceClient(ctx, devsyConfig, args)
	if err != nil {
		return err
	}

	_, agentInfo, err := workspaceClient.AgentInfo(provider2.CLIOptions{})
	if err != nil {
		return err
	}

	command := fmt.Sprintf(
		"%s internal agent workspace logs-daemon --context %s --id %s",
		shellescape.Quote(workspaceClient.AgentPath()),
		shellescape.Quote(workspaceClient.Context()),
		shellescape.Quote(workspaceClient.Workspace()),
	)
	if agentInfo.Agent.DataPath != "" {
		command += " --agent-dir " + shellescape.Quote(agentInfo.Agent.DataPath)
	}

	var stdout bytes.Buffer
	if err := workspaceClient.Command(ctx, client.CommandOptions{
		Command: command,
		Stdout:  &stdout,
		Stderr:  os.Stderr,
	}); err != nil {
		return err
	}

	var response machinediagnostics.ReadResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		return fmt.Errorf("decode remote daemon diagnostics: %w", err)
	}
	printDaemonDiagnostics(response)
	return nil
}

func (cmd *LogsDaemonCmd) machineWorkspaceClient(
	ctx context.Context,
	devsyConfig *config.Config,
	args []string,
) (client.WorkspaceClient, error) {
	baseClient, err := workspace.Get(ctx, workspace.GetOptions{
		DevsyConfig: devsyConfig,
		Args:        args,
		Owner:       cmd.Owner,
	})
	if err != nil {
		return nil, err
	} else if baseClient.WorkspaceConfig().Machine.ID == "" {
		return nil, fmt.Errorf(
			"selected workspace is not a machine provider, there is not daemon running",
		)
	}

	workspaceClient, ok := baseClient.(client.WorkspaceClient)
	if !ok {
		return nil, fmt.Errorf("this command is not supported for proxy providers")
	}
	return workspaceClient, nil
}

func printDaemonDiagnostics(response machinediagnostics.ReadResponse) {
	for _, event := range response.Events {
		_, _ = fmt.Fprintf(
			os.Stdout,
			"%s %-5s %-35s %s\n",
			event.Timestamp.Format(time.RFC3339),
			event.Level,
			event.Type,
			event.Message,
		)
	}
	if response.Availability != machinediagnostics.AvailabilityAvailable {
		_, _ = fmt.Fprintf(os.Stdout, "Daemon diagnostics: %s\n", response.Availability)
	}
}
