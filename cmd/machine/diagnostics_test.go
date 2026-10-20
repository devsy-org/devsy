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
	result, err := fetchDiagnostics(
		context.Background(),
		machine,
		diagnosticsFetchOptions{Limit: 20, IncludeEvents: true},
	)
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
		Events: []machinediagnostics.Event{
			{
				SessionID: "session",
				Sequence:  1,
				Timestamp: now,
				Type:      machinediagnostics.EventDaemonReady,
				Message:   "ready",
			},
		},
		Cursor: machinediagnostics.CursorInfo{
			State: machinediagnostics.CursorOK,
			Next:  machinediagnostics.EncodeCursor("session", 1),
		},
	})
	require.NoError(t, err)
	machine := &fakeMachineClient{status: client.StatusRunning, response: string(response)}
	result, err := fetchDiagnostics(
		context.Background(),
		machine,
		diagnosticsFetchOptions{Limit: 20, IncludeEvents: true},
	)
	require.NoError(t, err)
	assert.Equal(t, "available", result.Source.Availability)
	assert.Equal(t, machinediagnostics.DaemonHealthy, result.Daemon.Health)
	assert.Len(t, result.Events, 1)
	assert.Contains(t, machine.command, "daemon-diagnostics --limit 20")
}

func TestFetchDiagnosticsRetainsAvailabilityWhenRemoteCommandFails(t *testing.T) {
	machine := &fakeMachineClient{status: client.StatusRunning, commandErr: errors.New("ssh lost")}
	result, err := fetchDiagnostics(
		context.Background(),
		machine,
		diagnosticsFetchOptions{Limit: 20, IncludeEvents: true},
	)
	require.NoError(t, err)
	assert.Equal(t, "unavailable", result.Source.Availability)
	assert.Equal(t, "remote_diagnostics_command_failed", result.Source.ErrorCode)
}

func TestFetchDiagnosticsRejectsIncompleteResponse(t *testing.T) {
	machine := &fakeMachineClient{status: client.StatusRunning, response: `{}`}
	result, err := fetchDiagnostics(
		context.Background(),
		machine,
		diagnosticsFetchOptions{Limit: 20, IncludeEvents: true},
	)
	require.NoError(t, err)
	assert.Equal(t, "unavailable", result.Source.Availability)
	assert.Equal(t, "invalid_diagnostics_response", result.Source.ErrorCode)
}

func TestFetchDiagnosticsReportsProviderFailureWithoutEndingCollection(t *testing.T) {
	machine := &fakeMachineClient{statusErr: errors.New("provider connection lost")}
	result, err := fetchDiagnostics(
		context.Background(),
		machine,
		diagnosticsFetchOptions{Limit: 20, IncludeEvents: true},
	)
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
		{
			ID:             "api",
			State:          machinediagnostics.WorkspaceActive,
			LastActivityAt: &now,
			IdleDeadlineAt: &deadline,
		},
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

type renderDiagnosticsTextCase struct {
	name   string
	result MachineDiagnostics
	want   string
}

func runRenderDiagnosticsTextCases(t *testing.T, tests []renderDiagnosticsTextCase) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			require.NoError(t, renderDiagnosticsText(&output, tt.result))
			assert.Equal(t, tt.want, output.String())
		})
	}
}

func diagnosticsTestTime(hour, minute int) time.Time {
	return time.Date(2026, 10, 18, hour, minute, 0, 0, time.UTC)
}

func diagnosticsLocalClock(at time.Time) string {
	return at.Local().Format("2006-01-02 15:04:05")
}

const diagnosticsTestWorkspaceID = "api"

func diagnosticsTestFixture() (MachineInfo, CollectionSource) {
	return MachineInfo{ID: "machine-1", State: "running"},
		CollectionSource{Availability: "available", Freshness: "fresh"}
}

func TestRenderDiagnosticsTextWithoutDaemon(t *testing.T) {
	machine, source := diagnosticsTestFixture()
	header := "Machine            machine-1\nProvider state     running\nDiagnostics        available\n"

	runRenderDiagnosticsTextCases(t, []renderDiagnosticsTextCase{
		{
			name:   "no daemon",
			result: MachineDiagnostics{Machine: machine, Source: source},
			want:   header,
		},
		{
			name: "no daemon with diagnostic message",
			result: MachineDiagnostics{
				Machine: machine,
				Source: CollectionSource{
					Availability: "unavailable",
					Freshness:    "unknown",
					Message:      "agent offline",
				},
			},
			want: "Machine            machine-1\nProvider state     running\nDiagnostics        unavailable\n" +
				"Diagnostic detail  agent offline\n",
		},
	})
}

func TestRenderDiagnosticsTextWithDaemon(t *testing.T) {
	machine, source := diagnosticsTestFixture()
	lastPatrol := diagnosticsTestTime(10, 0)
	daemonHeader := "Machine            machine-1\nProvider state     running\nDiagnostics        available\n" +
		"Devsy daemon       healthy\nSnapshot freshness fresh\n"

	runRenderDiagnosticsTextCases(t, []renderDiagnosticsTextCase{
		{
			name: "daemon without patrol",
			result: MachineDiagnostics{
				Machine: machine,
				Source:  source,
				Daemon:  &machinediagnostics.Status{Health: machinediagnostics.DaemonHealthy},
			},
			want: daemonHeader + "Last patrol        Not yet observed\nWorkspaces         0\n",
		},
		{
			name: "shutdown candidate without eligibility time",
			result: MachineDiagnostics{
				Machine: machine,
				Source:  source,
				Daemon: &machinediagnostics.Status{
					Health:         machinediagnostics.DaemonHealthy,
					LastPatrolAt:   &lastPatrol,
					WorkspaceCount: 1,
					ShutdownCandidate: &machinediagnostics.ShutdownCandidate{
						WorkspaceID: diagnosticsTestWorkspaceID,
					},
				},
			},
			want: daemonHeader + "Last patrol        " + diagnosticsLocalClock(lastPatrol) + "\n" +
				"Workspaces         1\n" +
				"Shutdown candidate " + diagnosticsTestWorkspaceID + " (eligible now)\n",
		},
	})
}

func TestRenderDiagnosticsTextFullReport(t *testing.T) {
	lastPatrol := diagnosticsTestTime(10, 0)
	lastActivity := diagnosticsTestTime(9, 45)
	deadline := diagnosticsTestTime(10, 30)
	want := "Machine            machine-1\nProvider state     running\nDiagnostics        available\n" +
		"Devsy daemon       degraded\n" +
		"Snapshot freshness fresh\n" +
		"Last patrol        " + diagnosticsLocalClock(
		lastPatrol,
	) + "\n" +
		"Workspaces         2\n" +
		"Last daemon error  provider timeout (patrol_failed, 2026-10-18T09:55:00Z)\n" +
		"Shutdown candidate " + diagnosticsTestWorkspaceID + " (eligible " + diagnosticsLocalClock(
		deadline,
	) + ")\n" +
		"\nWorkspace inactivity:\n" +
		"WORKSPACE  STATE   LAST ACTIVITY        MACHINE SHUTDOWN STATUS\n" +
		diagnosticsTestWorkspaceID + "        active  " + diagnosticsLocalClock(
		lastActivity,
	) + "  " +
		diagnosticsLocalClock(
			deadline,
		) + "\n" +
		"docs       busy    -                    Delayed while workspace is busy\n" +
		"2026-10-18T10:05:00Z warn  patrol.failed provider timeout\n"

	runRenderDiagnosticsTextCases(t, []renderDiagnosticsTextCase{
		{
			name:   "daemon with error, eligible candidate, workspaces, and events",
			result: diagnosticsFullReportResult(lastPatrol, lastActivity, deadline),
			want:   want,
		},
	})
}

func diagnosticsFullReportResult(lastPatrol, lastActivity, deadline time.Time) MachineDiagnostics {
	machine, source := diagnosticsTestFixture()
	errorAt := diagnosticsTestTime(9, 55)
	eventAt := diagnosticsTestTime(10, 5)
	return MachineDiagnostics{
		Machine: machine,
		Source:  source,
		Daemon: &machinediagnostics.Status{
			Health:         machinediagnostics.DaemonDegraded,
			LastPatrolAt:   &lastPatrol,
			WorkspaceCount: 2,
			LastError: &machinediagnostics.DiagnosticError{
				Code:      "patrol_failed",
				Message:   "provider timeout",
				Timestamp: errorAt,
			},
			ShutdownCandidate: &machinediagnostics.ShutdownCandidate{
				WorkspaceID: diagnosticsTestWorkspaceID,
				EligibleAt:  &deadline,
			},
			Workspaces: []machinediagnostics.WorkspaceStatus{
				{
					ID:             diagnosticsTestWorkspaceID,
					State:          machinediagnostics.WorkspaceActive,
					LastActivityAt: &lastActivity,
					IdleDeadlineAt: &deadline,
				},
				{ID: "docs", State: machinediagnostics.WorkspaceBusy},
			},
		},
		Events: []machinediagnostics.Event{
			{
				SchemaVersion: machinediagnostics.SchemaVersion,
				SessionID:     "session-1",
				Sequence:      1,
				Timestamp:     eventAt,
				Level:         machinediagnostics.LevelWarn,
				Type:          machinediagnostics.EventPatrolFailed,
				Message:       "provider timeout",
			},
		},
	}
}

func TestWorkspaceAutoStopDetailMarksPastDeadlineEligibleNow(t *testing.T) {
	deadline := time.Now().Add(-time.Minute)
	detail := workspaceAutoStopDetail(machinediagnostics.WorkspaceStatus{
		State:          machinediagnostics.WorkspaceIdleDue,
		IdleDeadlineAt: &deadline,
	})
	assert.Contains(t, detail, "Eligible now")
}
