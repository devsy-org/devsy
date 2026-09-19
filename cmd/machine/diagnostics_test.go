package machine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/machinediagnostics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeMachineClient struct {
	client.MachineClient
	status       client.Status
	statusErr    error
	commandErr   error
	command      string
	commandCalls int
	response     string
}

func (f *fakeMachineClient) Machine() string   { return "machine-1" }
func (f *fakeMachineClient) Context() string   { return "default" }
func (f *fakeMachineClient) Provider() string  { return "fake" }
func (f *fakeMachineClient) AgentPath() string { return "/usr/local/bin/devsy" }
func (f *fakeMachineClient) Status(context.Context, client.StatusOptions) (client.Status, error) {
	return f.status, f.statusErr
}
func (f *fakeMachineClient) Command(_ context.Context, options client.CommandOptions) error {
	f.commandCalls++
	f.command = options.Command
	if f.commandErr != nil {
		return f.commandErr
	}
	_, _ = options.Stdout.Write([]byte(f.response))
	return nil
}

func TestBoundedDiagnosticsBuffer(t *testing.T) {
	buffer := boundedDiagnosticsBuffer{max: 4}
	_, err := buffer.Write([]byte("test"))
	require.NoError(t, err)
	_, err = buffer.Write([]byte("x"))
	require.ErrorContains(t, err, "exceeds")
	assert.Equal(t, "test", buffer.String())
}

func TestFetchDiagnosticsSkipsRemoteCommandForStoppedMachine(t *testing.T) {
	machine := &fakeMachineClient{status: client.StatusStopped}
	result, err := fetchDiagnostics(context.Background(), machine, "", 20, true)
	require.NoError(t, err)
	assert.Equal(t, "machine_stopped", result.Source.Availability)
	assert.Zero(t, machine.commandCalls)
}

func TestFetchDiagnosticsReadsRemoteResponse(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	response, err := json.Marshal(machinediagnostics.ReadResponse{
		SchemaVersion: machinediagnostics.SchemaVersion,
		Availability:  machinediagnostics.AvailabilityAvailable,
		Freshness:     machinediagnostics.FreshnessFresh,
		Status:        &machinediagnostics.Status{Health: machinediagnostics.DaemonHealthy},
		Events:        []machinediagnostics.Event{{SessionID: "session", Sequence: 1, Timestamp: now, Type: machinediagnostics.EventDaemonReady, Message: "ready"}},
		Cursor:        machinediagnostics.CursorInfo{State: machinediagnostics.CursorOK, Next: machinediagnostics.EncodeCursor("session", 1)},
	})
	require.NoError(t, err)
	machine := &fakeMachineClient{status: client.StatusRunning, response: string(response)}
	result, err := fetchDiagnostics(context.Background(), machine, "", 20, true)
	require.NoError(t, err)
	assert.Equal(t, "available", result.Source.Availability)
	assert.Equal(t, machinediagnostics.DaemonHealthy, result.Daemon.Health)
	assert.Len(t, result.Events, 1)
	assert.Contains(t, machine.command, "daemon-diagnostics --limit 20")
}

func TestFetchDiagnosticsRetainsAvailabilityWhenRemoteCommandFails(t *testing.T) {
	machine := &fakeMachineClient{status: client.StatusRunning, commandErr: errors.New("ssh lost")}
	result, err := fetchDiagnostics(context.Background(), machine, "", 20, true)
	require.NoError(t, err)
	assert.Equal(t, "unavailable", result.Source.Availability)
	assert.Equal(t, "remote_diagnostics_command_failed", result.Source.ErrorCode)
}

func TestFetchDiagnosticsRejectsIncompleteResponse(t *testing.T) {
	machine := &fakeMachineClient{status: client.StatusRunning, response: `{}`}
	result, err := fetchDiagnostics(context.Background(), machine, "", 20, true)
	require.NoError(t, err)
	assert.Equal(t, "unavailable", result.Source.Availability)
	assert.Equal(t, "invalid_diagnostics_response", result.Source.ErrorCode)
}

func TestFetchDiagnosticsReportsProviderFailureWithoutEndingCollection(t *testing.T) {
	machine := &fakeMachineClient{statusErr: errors.New("provider connection lost")}
	result, err := fetchDiagnostics(context.Background(), machine, "", 20, true)
	require.NoError(t, err)
	assert.Equal(t, "unknown", result.Machine.State)
	assert.Equal(t, "machine_status_unavailable", result.Source.ErrorCode)
	assert.Zero(t, machine.commandCalls)
}

func TestRenderDiagnosticsTextIncludesWorkspaceInactivityDetails(t *testing.T) {
	now := time.Date(2026, 9, 19, 1, 30, 0, 0, time.Local)
	deadline := now.Add(30 * time.Minute)
	var output bytes.Buffer
	renderWorkspaceDiagnostics(&output, []machinediagnostics.WorkspaceStatus{
		{ID: "api", State: machinediagnostics.WorkspaceActive, LastActivityAt: &now, IdleDeadlineAt: &deadline},
		{ID: "docs", State: machinediagnostics.WorkspaceBusy},
		{ID: "preview", State: machinediagnostics.WorkspaceNotConfigured},
		{ID: "broken", State: machinediagnostics.WorkspaceInvalidConfig},
	})
	text := output.String()
	assert.Contains(t, text, "Workspace inactivity:")
	assert.Contains(t, text, "MACHINE SHUTDOWN STATUS")
	assert.Contains(t, text, "api")
	assert.Contains(t, text, deadline.Format("2006-01-02 15:04:05"))
	assert.Contains(t, text, "Delayed while workspace is busy")
	assert.Contains(t, text, "Auto-stop is not configured")
	assert.Contains(t, text, "Workspace configuration is invalid")
}

func TestWorkspaceAutoStopDetailMarksPastDeadlineEligibleNow(t *testing.T) {
	deadline := time.Now().Add(-time.Minute)
	detail := workspaceAutoStopDetail(machinediagnostics.WorkspaceStatus{
		State:          machinediagnostics.WorkspaceIdleDue,
		IdleDeadlineAt: &deadline,
	})
	assert.Contains(t, detail, "Eligible now")
}
