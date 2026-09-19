package machine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"al.essio.dev/pkg/shellescape"
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/machinediagnostics"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

const maxRemoteDiagnosticsResponse = 2 * 1024 * 1024

type boundedDiagnosticsBuffer struct {
	bytes.Buffer
	max int
}

func (b *boundedDiagnosticsBuffer) Write(value []byte) (int, error) {
	if b.Len()+len(value) > b.max {
		return 0, fmt.Errorf("remote diagnostics response exceeds %d bytes", b.max)
	}
	return b.Buffer.Write(value)
}

type CollectionSource struct {
	Availability string `json:"availability"`
	Freshness    string `json:"freshness"`
	ErrorCode    string `json:"errorCode,omitempty"`
	Message      string `json:"message,omitempty"`
}
type MachineDiagnostics struct {
	SchemaVersion int `json:"schemaVersion"`
	Machine       struct {
		ID       string `json:"id"`
		Context  string `json:"context"`
		Provider string `json:"provider"`
		State    string `json:"state"`
	} `json:"machine"`
	Source CollectionSource              `json:"source"`
	Daemon *machinediagnostics.Status    `json:"daemon,omitempty"`
	Events []machinediagnostics.Event    `json:"events,omitempty"`
	Cursor machinediagnostics.CursorInfo `json:"cursor"`
}

type DiagnosticsCmd struct {
	*flags.GlobalFlags
	After    string
	Limit    int
	NoEvents bool
}

func NewDiagnosticsCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &DiagnosticsCmd{GlobalFlags: globalFlags}
	c := &cobra.Command{Use: "diagnostics [name]", Short: "Show Devsy machine diagnostic events and daemon status", RunE: func(c *cobra.Command, args []string) error { return cmd.Run(c.Context(), args) }}
	cliflags.Add(c, cliflags.String(&cmd.After, "after", "", "An opaque diagnostics cursor"), cliflags.Int(&cmd.Limit, "limit", 20, "Maximum diagnostic events"), cliflags.Bool(&cmd.NoEvents, "no-events", false, "Do not include diagnostic events"))
	return c
}
func (cmd *DiagnosticsCmd) Run(ctx context.Context, args []string) error {
	if cmd.Limit < 0 || cmd.Limit > machinediagnostics.MaxReadEvents {
		return fmt.Errorf("diagnostics limit must be between 0 and %d", machinediagnostics.MaxReadEvents)
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
	result, err := fetchDiagnostics(ctx, mc, cmd.After, cmd.Limit, !cmd.NoEvents)
	if err != nil {
		return err
	}
	return renderDiagnostics(result, cmd.ResultFormat)
}
func fetchDiagnostics(ctx context.Context, mc client.MachineClient, after string, limit int, includeEvents bool) (MachineDiagnostics, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result := MachineDiagnostics{SchemaVersion: machinediagnostics.SchemaVersion, Cursor: machinediagnostics.CursorInfo{State: machinediagnostics.CursorNone}}
	result.Machine.ID = mc.Machine()
	result.Machine.Context = mc.Context()
	result.Machine.Provider = mc.Provider()
	status, err := mc.Status(ctx, client.StatusOptions{})
	if err != nil {
		if ctx.Err() != nil && ctx.Err() != context.DeadlineExceeded {
			return result, ctx.Err()
		}
		result.Machine.State = "unknown"
		result.Source = CollectionSource{Availability: "unavailable", Freshness: "unknown", ErrorCode: "machine_status_unavailable", Message: "Devsy could not read the provider's machine state. Try again when the provider connection is available."}
		return result, nil
	}
	result.Machine.State = string(status)
	if !strings.EqualFold(string(status), string(client.StatusRunning)) {
		if status == client.StatusStopped {
			result.Source = CollectionSource{Availability: "machine_stopped", Freshness: "unknown"}
		} else {
			result.Source = CollectionSource{Availability: "unavailable", Freshness: "unknown", ErrorCode: "machine_not_running", Message: "The machine is not running."}
		}
		return result, nil
	}
	command := shellescape.Quote(mc.AgentPath()) + " internal agent daemon-diagnostics --limit " + strconv.Itoa(limit)
	if after != "" {
		command += " --after " + shellescape.Quote(after)
	}
	stdout := boundedDiagnosticsBuffer{max: maxRemoteDiagnosticsResponse}
	stderr := boundedDiagnosticsBuffer{max: maxRemoteDiagnosticsResponse}
	err = mc.Command(ctx, client.CommandOptions{Command: command, Stdout: &stdout, Stderr: &stderr})
	if err != nil {
		result.Source = CollectionSource{Availability: "unavailable", Freshness: "unknown", ErrorCode: "remote_diagnostics_command_failed", Message: "Devsy could not read remote diagnostics."}
		return result, nil
	}
	var remote machinediagnostics.ReadResponse
	if err := json.Unmarshal(stdout.Bytes(), &remote); err != nil {
		result.Source = CollectionSource{Availability: "unavailable", Freshness: "unknown", ErrorCode: "remote_diagnostics_command_failed", Message: "Remote diagnostics returned an invalid response."}
		return result, nil
	}
	if remote.SchemaVersion != machinediagnostics.SchemaVersion || remote.Availability == "" || remote.Freshness == "" {
		result.Source = CollectionSource{Availability: "unavailable", Freshness: "unknown", ErrorCode: "invalid_diagnostics_response", Message: "Remote diagnostics returned an unsupported or incomplete response."}
		return result, nil
	}
	result.Source = CollectionSource{Availability: string(remote.Availability), Freshness: string(remote.Freshness)}
	if remote.Error != nil {
		result.Source.ErrorCode = remote.Error.Code
		result.Source.Message = remote.Error.Message
	}
	result.Daemon = remote.Status
	if includeEvents {
		result.Events = remote.Events
	}
	result.Cursor = remote.Cursor
	return result, nil
}
func renderDiagnostics(result MachineDiagnostics, format string) error {
	mode, err := output.ResolveMode(format)
	if err != nil {
		return err
	}
	if mode == output.ModeJSON {
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	return renderDiagnosticsText(os.Stdout, result)
}

func renderDiagnosticsText(w io.Writer, result MachineDiagnostics) error {
	_, _ = fmt.Fprintf(w, "Machine            %s\nProvider state     %s\nDiagnostics        %s\n", result.Machine.ID, result.Machine.State, result.Source.Availability)
	if result.Source.Message != "" {
		_, _ = fmt.Fprintf(w, "Diagnostic detail  %s\n", result.Source.Message)
	}
	if result.Daemon != nil {
		lastPatrol := "Not yet observed"
		if result.Daemon.LastPatrolAt != nil {
			lastPatrol = result.Daemon.LastPatrolAt.Local().Format("2006-01-02 15:04:05")
		}
		_, _ = fmt.Fprintf(w, "Devsy daemon       %s\nSnapshot freshness %s\nLast patrol        %s\nWorkspaces         %d\n", result.Daemon.Health, result.Source.Freshness, lastPatrol, result.Daemon.WorkspaceCount)
		if result.Daemon.LastError != nil {
			_, _ = fmt.Fprintf(w, "Last daemon error  %s (%s, %s)\n", result.Daemon.LastError.Message, result.Daemon.LastError.Code, result.Daemon.LastError.Timestamp.Format(time.RFC3339))
		}
		if result.Daemon.ShutdownCandidate != nil {
			eligible := "now"
			if result.Daemon.ShutdownCandidate.EligibleAt != nil {
				eligible = result.Daemon.ShutdownCandidate.EligibleAt.Local().Format("2006-01-02 15:04:05")
			}
			_, _ = fmt.Fprintf(w, "Shutdown candidate %s (eligible %s)\n", result.Daemon.ShutdownCandidate.WorkspaceID, eligible)
		}
		renderWorkspaceDiagnostics(w, result.Daemon.Workspaces)
	}
	for _, event := range result.Events {
		_, _ = fmt.Fprintf(w, "%s %-5s %s %s\n", event.Timestamp.Format(time.RFC3339), event.Level, event.Type, event.Message)
	}
	return nil
}

func renderWorkspaceDiagnostics(w io.Writer, workspaces []machinediagnostics.WorkspaceStatus) {
	if len(workspaces) == 0 {
		return
	}
	_, _ = fmt.Fprintln(w, "\nWorkspace inactivity:")
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "WORKSPACE\tSTATE\tLAST ACTIVITY\tMACHINE SHUTDOWN STATUS")
	for _, workspace := range workspaces {
		lastActivity := "-"
		if workspace.LastActivityAt != nil {
			lastActivity = workspace.LastActivityAt.Local().Format("2006-01-02 15:04:05")
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", workspace.ID, workspace.State, lastActivity, workspaceAutoStopDetail(workspace))
	}
	_ = tw.Flush()
}

func workspaceAutoStopDetail(workspace machinediagnostics.WorkspaceStatus) string {
	if workspace.BlocksMachineShutdown && workspace.BlockerReason != "" {
		if workspace.State == machinediagnostics.WorkspaceActive && workspace.IdleDeadlineAt != nil {
			return workspace.BlockerReason + " (" + workspace.IdleDeadlineAt.Local().Format("2006-01-02 15:04:05") + ")"
		}
		return workspace.BlockerReason
	}
	if workspace.IdleDeadlineAt != nil {
		if workspace.State == machinediagnostics.WorkspaceIdleDue {
			return "Eligible now (" + workspace.IdleDeadlineAt.Local().Format("2006-01-02 15:04:05") + ")"
		}
		return workspace.IdleDeadlineAt.Local().Format("2006-01-02 15:04:05")
	}
	switch workspace.State {
	case machinediagnostics.WorkspaceBusy:
		return "Delayed while workspace is busy"
	case machinediagnostics.WorkspaceNotConfigured:
		return "Auto-stop is not configured"
	case machinediagnostics.WorkspaceInvalidConfig:
		return "Workspace configuration is invalid"
	case machinediagnostics.WorkspaceNotRunning:
		return "Workspace state is unavailable"
	default:
		return "-"
	}
}
