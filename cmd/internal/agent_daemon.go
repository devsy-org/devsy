package cmdinternal

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/agent"
	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	agentconfig "github.com/devsy-org/devsy/pkg/config"
	agentdaemon "github.com/devsy-org/devsy/pkg/daemon/agent"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver/custom"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/machinediagnostics"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/spf13/cobra"
)

const (
	defaultPatrolInterval = time.Minute
	busyGracePeriod       = 20 * time.Minute
)

var daemonRuntimeLockPath = machinediagnostics.DefaultRuntimeLockPath

type DaemonCmd struct {
	*flags.GlobalFlags

	Interval             string
	ShutdownAction       string
	StateRoot            string
	StateLayout          string
	DiagnosticsReaderUID int
	DiagnosticsReaderGID int

	recorder                 machinediagnostics.Recorder
	startedAt                time.Time
	lastSuccessfulPatrolAt   *time.Time
	lastPatrolAt             *time.Time
	lastDiagnosticError      *machinediagnostics.DiagnosticError
	knownWorkspaceIDs        map[string]struct{}
	knownInvalidWorkspaceIDs map[string]struct{}
}

func NewDaemonCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &DaemonCmd{GlobalFlags: flags}
	daemonCmd := &cobra.Command{
		Use:   "daemon",
		Short: "Watches for activity and stops the server due to inactivity",
		Args:  cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, _ []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}
	cliflags.Add(
		daemonCmd,
		cliflags.String(&cmd.Interval, names.Interval, "", "The interval how to poll workspaces"),
		cliflags.String(
			&cmd.ShutdownAction,
			names.ShutdownAction,
			"",
			"The shutdown action (none, stopContainer, or stopCompose)",
		),
		cliflags.String(&cmd.StateRoot, names.StateRoot, "", "The daemon workspace state root"),
		cliflags.String(&cmd.StateLayout, names.StateLayout, "", "The daemon workspace state layout"),
		cliflags.Int(&cmd.DiagnosticsReaderUID, names.DiagnosticsReaderUID, -1, "The diagnostics reader UID"),
		cliflags.Int(&cmd.DiagnosticsReaderGID, names.DiagnosticsReaderGID, -1, "The diagnostics reader GID"),
	)
	_ = daemonCmd.Flags().MarkHidden(names.StateRoot)
	_ = daemonCmd.Flags().MarkHidden(names.StateLayout)
	_ = daemonCmd.Flags().MarkHidden(names.DiagnosticsReaderUID)
	_ = daemonCmd.Flags().MarkHidden(names.DiagnosticsReaderGID)
	return daemonCmd
}

func (cmd *DaemonCmd) Run(ctx context.Context) error {
	location, err := cmd.stateLocation()
	if err != nil {
		return err
	}
	runtimeLock, err := machinediagnostics.AcquireRuntimeLock(daemonRuntimeLockPath)
	if err != nil {
		return err
	}
	defer func() { _ = runtimeLock.Close() }()
	cmd.startedAt = time.Now().UTC()
	recorder, err := machinediagnostics.NewRecorder(machinediagnostics.Options{
		Dir:            machinediagnostics.DiagnosticsDir(location.Root),
		Reader:         cmd.diagnosticsReader(),
		PatrolInterval: cmd.pollInterval(),
		ErrorReporter: func(err error) {
			log.Warnf("write machine diagnostics: %v", err)
		},
	})
	if err != nil {
		log.Warnf("initialize machine diagnostics: %v", err)
		cmd.recorder = machinediagnostics.Nop()
	} else {
		cmd.recorder = recorder
		if err := machinediagnostics.WriteLocator(machinediagnostics.DefaultLocatorPath, machinediagnostics.Locator{
			SessionID: recorder.SessionID(), DiagnosticsDir: machinediagnostics.DiagnosticsDir(location.Root),
			StateLayout: string(location.Layout), StartedAt: cmd.startedAt,
		}); err != nil {
			log.Warnf("write machine diagnostics locator: %v", err)
		}
	}
	cmd.recordEvent(machinediagnostics.Event{Type: machinediagnostics.EventDaemonStarted, Level: machinediagnostics.LevelInfo, Message: "Devsy machine daemon started."})
	cmd.updateDiagnostics(machinediagnostics.DaemonStarting, machinediagnostics.DaemonHealthy, nil, nil, nil)
	defer func() {
		cmd.recordEvent(machinediagnostics.Event{Type: machinediagnostics.EventDaemonStopping, Level: machinediagnostics.LevelInfo, Message: "Devsy machine daemon stopping."})
		cmd.updateDiagnostics(machinediagnostics.DaemonStopping, machinediagnostics.DaemonHealthy, nil, nil, nil)
		_ = cmd.recorder.Close()
	}()

	log.Infof("starting Devsy daemon patrol at %s", location.Root)
	cmd.patrol(ctx)
	return nil
}

func (cmd *DaemonCmd) stateLocation() (agentdaemon.StateLocation, error) {
	if cmd.StateRoot != "" || cmd.StateLayout != "" {
		if cmd.StateRoot == "" || cmd.StateLayout == "" {
			return agentdaemon.StateLocation{}, fmt.Errorf(
				"daemon state root and state layout must be provided together",
			)
		}
		location := agentdaemon.StateLocation{
			Root:   filepath.Clean(cmd.StateRoot),
			Layout: agentdaemon.StateLayout(cmd.StateLayout),
		}
		return location, location.Validate()
	}

	if cmd.AgentDir != "" {
		location := agentdaemon.StateLocation{
			Root:   filepath.Clean(cmd.AgentDir),
			Layout: agentdaemon.StateLayoutAgentHome,
		}
		return location, location.Validate()
	}

	return agentdaemon.StateLocation{}, fmt.Errorf("daemon state location is missing")
}

func (cmd *DaemonCmd) patrol(ctx context.Context) {
	cmd.initialTouch()
	cmd.recordEvent(machinediagnostics.Event{Type: machinediagnostics.EventDaemonReady, Level: machinediagnostics.LevelInfo, Message: "Devsy machine daemon is ready."})

	ticker := time.NewTicker(cmd.pollInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cmd.patrolOnce(ctx)
		}
	}
}

func (cmd *DaemonCmd) diagnosticsReader() machinediagnostics.ReaderIdentity {
	if cmd.DiagnosticsReaderUID >= 0 && cmd.DiagnosticsReaderGID >= 0 {
		return machinediagnostics.ReaderIdentity{UID: cmd.DiagnosticsReaderUID, GID: cmd.DiagnosticsReaderGID}
	}
	return agentdaemon.DiagnosticsReaderIdentity()
}

func (cmd *DaemonCmd) recordEvent(event machinediagnostics.Event) {
	if cmd.recorder != nil {
		cmd.recorder.Record(event)
	}
}

func (cmd *DaemonCmd) updateDiagnostics(state machinediagnostics.DaemonState, health machinediagnostics.DaemonHealth, diagnosticErr *machinediagnostics.DiagnosticError, workspaces []machinediagnostics.WorkspaceStatus, candidate *machinediagnostics.ShutdownCandidate) {
	if cmd.recorder == nil {
		return
	}
	now := time.Now().UTC()
	if diagnosticErr != nil {
		cmd.lastDiagnosticError = diagnosticErr
	}
	if state == machinediagnostics.DaemonRunning {
		cmd.lastPatrolAt = &now
	}
	if health == machinediagnostics.DaemonHealthy && state == machinediagnostics.DaemonRunning {
		cmd.lastSuccessfulPatrolAt = &now
	}
	cmd.recorder.Update(machinediagnostics.Status{StartedAt: cmd.startedAt, UpdatedAt: now, State: state, Health: health, PatrolInterval: cmd.pollInterval().String(), LastPatrolAt: cmd.lastPatrolAt, LastSuccessAt: cmd.lastSuccessfulPatrolAt, LastError: cmd.lastDiagnosticError, WorkspaceCount: len(workspaces), Workspaces: workspaces, ShutdownCandidate: candidate})
}

func (cmd *DaemonCmd) pollInterval() time.Duration {
	if cmd.Interval == "" {
		return defaultPatrolInterval
	}
	parsed, err := time.ParseDuration(cmd.Interval)
	if err != nil {
		log.Errorf("parse interval %q, using %s: %v", cmd.Interval, defaultPatrolInterval, err)
		return defaultPatrolInterval
	}
	if parsed <= 0 {
		log.Errorf("non-positive interval %q, using %s", cmd.Interval, defaultPatrolInterval)
		return defaultPatrolInterval
	}
	return parsed
}

func (cmd *DaemonCmd) workspaceConfigs() (baseDir string, configs []string, err error) {
	location, err := cmd.stateLocation()
	if err != nil {
		return "", nil, err
	}
	pattern, err := location.WorkspaceConfigPattern()
	if err != nil {
		return "", nil, err
	}
	configs, err = filepath.Glob(pattern)
	if err != nil {
		return "", nil, fmt.Errorf("glob %s: %w", pattern, err)
	}
	return location.Root, configs, nil
}

func (cmd *DaemonCmd) patrolOnce(ctx context.Context) {
	baseDir, configs, err := cmd.workspaceConfigs()
	if err != nil {
		log.Errorf("list workspace configs: %v", err)
		cmd.recordEvent(machinediagnostics.Event{Type: machinediagnostics.EventPatrolFailed, Level: machinediagnostics.LevelError, Message: "Machine daemon could not discover workspace state.", ErrorCode: "patrol_failed"})
		cmd.updateDiagnostics(machinediagnostics.DaemonRunning, machinediagnostics.DaemonDegraded, &machinediagnostics.DiagnosticError{Code: "patrol_failed", Message: "Machine daemon could not discover workspace state.", Timestamp: time.Now().UTC()}, nil, nil)
		return
	}
	evaluation := evaluateMachineInactivity(configs, activityHeartbeat(), cmd.ShutdownAction, time.Now())
	cmd.recordWorkspaceTransitions(evaluation.statuses)
	cmd.updateDiagnostics(machinediagnostics.DaemonRunning, machinediagnostics.DaemonHealthy, nil, evaluation.statuses, evaluation.candidate)
	if evaluation.workspace == nil {
		log.Infof("no machine shutdown candidate in %q: %s", baseDir, evaluation.reason)
		return
	}
	if err := cmd.shutdownWorkspace(ctx, evaluation.workspace); err != nil {
		cmd.updateDiagnostics(machinediagnostics.DaemonRunning, machinediagnostics.DaemonDegraded, &machinediagnostics.DiagnosticError{Code: "shutdown_failed", Message: "Machine shutdown action failed.", Timestamp: time.Now().UTC()}, evaluation.statuses, evaluation.candidate)
	}
}

func (cmd *DaemonCmd) recordWorkspaceTransitions(statuses []machinediagnostics.WorkspaceStatus) {
	current := make(map[string]struct{}, len(statuses))
	invalid := make(map[string]struct{})
	for _, status := range statuses {
		if status.ID == "" {
			continue
		}
		current[status.ID] = struct{}{}
		if _, known := cmd.knownWorkspaceIDs[status.ID]; !known {
			cmd.recordEvent(machinediagnostics.Event{Type: machinediagnostics.EventWorkspaceDiscovered, Level: machinediagnostics.LevelInfo, WorkspaceID: status.ID, Message: "Workspace state was discovered."})
		}
		if status.State == machinediagnostics.WorkspaceInvalidConfig {
			invalid[status.ID] = struct{}{}
			if _, alreadyReported := cmd.knownInvalidWorkspaceIDs[status.ID]; !alreadyReported {
				cmd.recordEvent(machinediagnostics.Event{Type: machinediagnostics.EventWorkspaceConfigErr, Level: machinediagnostics.LevelWarn, WorkspaceID: status.ID, Message: "Workspace state could not be parsed.", ErrorCode: "workspace_config_invalid"})
			}
		}
	}
	for id := range cmd.knownWorkspaceIDs {
		if _, stillPresent := current[id]; !stillPresent {
			cmd.recordEvent(machinediagnostics.Event{Type: machinediagnostics.EventWorkspaceRemoved, Level: machinediagnostics.LevelInfo, WorkspaceID: id, Message: "Workspace state is no longer present."})
		}
	}
	cmd.knownWorkspaceIDs = current
	cmd.knownInvalidWorkspaceIDs = invalid
}

var activityFilePath = agentconfig.ContainerActivityFile

func effectiveActivity(configActivity time.Time) time.Time {
	if hb := activityHeartbeat(); hb.After(configActivity) {
		return hb
	}
	return configActivity
}

func activityHeartbeat() time.Time {
	stat, err := os.Stat(activityFilePath)
	if err != nil {
		return time.Time{}
	}
	return stat.ModTime()
}

func (cmd *DaemonCmd) shutdownWorkspace(ctx context.Context, workspace *provider2.AgentWorkspaceInfo) error {
	cmd.recordEvent(machinediagnostics.Event{Type: machinediagnostics.EventIdleDeadline, Level: machinediagnostics.LevelInfo, WorkspaceID: workspace.Workspace.ID, Message: "Workspace inactivity deadline was reached."})
	cmd.recordEvent(machinediagnostics.Event{Type: machinediagnostics.EventShutdownStarted, Level: machinediagnostics.LevelInfo, WorkspaceID: workspace.Workspace.ID, Message: "Machine shutdown action started."})
	if err := cmd.runShutdownCommand(ctx, workspace); err != nil {
		cmd.recordEvent(machinediagnostics.Event{Type: machinediagnostics.EventShutdownFailed, Level: machinediagnostics.LevelError, WorkspaceID: workspace.Workspace.ID, Message: "Machine shutdown action failed.", ErrorCode: "shutdown_failed"})
		return err
	}
	cmd.recordEvent(machinediagnostics.Event{Type: machinediagnostics.EventShutdownSucceeded, Level: machinediagnostics.LevelInfo, WorkspaceID: workspace.Workspace.ID, Message: "Machine shutdown command completed; provider state confirms whether the machine stopped."})
	return nil
}

// effectiveShutdownAction prefers the workspace's resolved config, falling back
// to the daemon's install-time flag when the workspace has none yet.
func (cmd *DaemonCmd) effectiveShutdownAction(
	workspace *provider2.AgentWorkspaceInfo,
) string {
	if workspace != nil &&
		workspace.LastDevContainerConfig != nil &&
		workspace.LastDevContainerConfig.Config != nil &&
		workspace.LastDevContainerConfig.Config.ShutdownAction != "" {
		return workspace.LastDevContainerConfig.Config.ShutdownAction
	}
	return cmd.ShutdownAction
}

func (cmd *DaemonCmd) runShutdownCommand(
	ctx context.Context,
	workspace *provider2.AgentWorkspaceInfo,
) error {
	environ, err := custom.ToEnvironWithBinaries(ctx, workspace)
	if err != nil {
		log.Errorf("build shutdown environment: %v", err)
		return err
	}

	shutdown := strings.Join(workspace.Agent.Exec.Shutdown, " ")
	log.Infof("running shutdown command for workspace %s: %s", workspace.Workspace.ID, shutdown)

	var stdout, stderr bytes.Buffer
	err = clientimplementation.RunCommand(ctx, clientimplementation.RunCommandOptions{
		Command: workspace.Agent.Exec.Shutdown,
		Environ: environ,
		Stdout:  &stdout,
		Stderr:  &stderr,
	})
	if err != nil {
		log.Errorf(
			"run shutdown command %s: %v (stdout: %s, stderr: %s)",
			shutdown, err, stdout.String(), stderr.String(),
		)
		return err
	}

	log.Infof("ran shutdown command (stdout: %s, stderr: %s)", stdout.String(), stderr.String())
	return nil
}

func (cmd *DaemonCmd) initialTouch() {
	_, configs, err := cmd.workspaceConfigs()
	if err != nil {
		log.Errorf("list workspace configs: %v", err)
		return
	}

	now := time.Now()
	for _, cfg := range configs {
		if err := os.Chtimes(cfg, now, now); err != nil {
			log.Errorf("touch workspace config %s: %v", cfg, err)
		}
	}
}

func findLatestActivity(configs []string) (*time.Time, *provider2.AgentWorkspaceInfo) {
	var latestActivity *time.Time
	var workspace *provider2.AgentWorkspaceInfo
	for _, cfg := range configs {
		activity, activityWorkspace, err := getActivity(cfg)
		if err != nil {
			log.Errorf("check inactivity for %s: %v", cfg, err)
			continue
		}
		if activity == nil {
			continue
		}
		if latestActivity == nil || activity.After(*latestActivity) {
			latestActivity = activity
			workspace = activityWorkspace
		}
	}
	return latestActivity, workspace
}

func getActivity(workspaceConfig string) (*time.Time, *provider2.AgentWorkspaceInfo, error) {
	workspace, err := agent.ParseAgentWorkspaceInfo(workspaceConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", workspaceConfig, err)
	}
	if len(workspace.Agent.Exec.Shutdown) == 0 {
		return nil, nil, nil
	}

	stat, err := os.Stat(workspaceConfig)
	if err != nil {
		return nil, nil, err
	}

	activity := stat.ModTime()
	if agent.HasWorkspaceBusyFile(filepath.Dir(workspaceConfig)) {
		activity = activity.Add(busyGracePeriod)
	}
	return &activity, workspace, nil
}

// diagnosticsWorkspaceStatuses observes every discovered configuration for the
// status snapshot. It deliberately does not participate in selecting the one
// latest workspace that controls existing shutdown semantics.
type machineInactivityEvaluation struct {
	statuses  []machinediagnostics.WorkspaceStatus
	workspace *provider2.AgentWorkspaceInfo
	candidate *machinediagnostics.ShutdownCandidate
	reason    string
}

type evaluatedWorkspace struct {
	status    machinediagnostics.WorkspaceStatus
	workspace *provider2.AgentWorkspaceInfo
}

func evaluateMachineInactivity(configs []string, heartbeat time.Time, fallbackAction string, now time.Time) machineInactivityEvaluation {
	evaluated := make([]evaluatedWorkspace, 0, len(configs))
	for _, path := range configs {
		evaluated = append(evaluated, evaluateWorkspaceInactivity(path, heartbeat, fallbackAction, now))
	}
	result := machineInactivityEvaluation{statuses: make([]machinediagnostics.WorkspaceStatus, 0, len(evaluated))}
	for _, item := range evaluated {
		result.statuses = append(result.statuses, item.status)
	}
	for _, item := range evaluated {
		if item.status.BlocksMachineShutdown {
			result.reason = item.status.BlockerReason
			return result
		}
	}
	if len(evaluated) == 0 {
		result.reason = "no workspace state was discovered"
		return result
	}
	for _, item := range evaluated {
		if result.workspace == nil || item.status.IdleDeadlineAt.After(*result.candidate.EligibleAt) || (item.status.IdleDeadlineAt.Equal(*result.candidate.EligibleAt) && item.status.ID < result.candidate.WorkspaceID) {
			deadline := *item.status.IdleDeadlineAt
			result.workspace = item.workspace
			result.candidate = &machinediagnostics.ShutdownCandidate{WorkspaceID: item.status.ID, EligibleAt: &deadline}
		}
	}
	result.reason = "all workspaces are idle"
	return result
}

func evaluateWorkspaceInactivity(path string, heartbeat time.Time, fallbackAction string, now time.Time) evaluatedWorkspace {
	workspace, err := agent.ParseAgentWorkspaceInfo(path)
	if err != nil || workspace.Workspace == nil {
		return evaluatedWorkspace{status: blockedWorkspaceStatus(workspaceIDFromConfig(path), machinediagnostics.WorkspaceInvalidConfig, "Workspace configuration is invalid")}
	}
	status := machinediagnostics.WorkspaceStatus{ID: workspace.Workspace.ID}
	stat, err := os.Stat(path)
	if err != nil {
		return evaluatedWorkspace{status: blockedWorkspaceStatus(status.ID, machinediagnostics.WorkspaceNotRunning, "Workspace state is unavailable"), workspace: workspace}
	}
	action := fallbackAction
	if workspace.LastDevContainerConfig != nil && workspace.LastDevContainerConfig.Config != nil && workspace.LastDevContainerConfig.Config.ShutdownAction != "" {
		action = workspace.LastDevContainerConfig.Config.ShutdownAction
	}
	status.ShutdownActionEnabled = len(workspace.Agent.Exec.Shutdown) > 0 && action != config.ShutdownActionNone
	if !status.ShutdownActionEnabled {
		return evaluatedWorkspace{status: blockedWorkspaceStatus(status.ID, machinediagnostics.WorkspaceNotConfigured, "Auto-stop is not configured"), workspace: workspace}
	}
	activity := stat.ModTime()
	if heartbeat.After(activity) {
		activity = heartbeat
	}
	status.LastActivityAt = &activity
	timeout := agent.DefaultInactivityTimeout
	if workspace.Agent.Timeout != "" {
		parsed, parseErr := time.ParseDuration(workspace.Agent.Timeout)
		if parseErr != nil || parsed <= 0 {
			return evaluatedWorkspace{status: blockedWorkspaceStatus(status.ID, machinediagnostics.WorkspaceInvalidConfig, "Inactivity timeout must be a positive duration"), workspace: workspace}
		}
		timeout = parsed
	}
	status.Timeout = timeout.String()
	status.Busy = agent.HasWorkspaceBusyFile(filepath.Dir(path))
	if status.Busy {
		deadline := activity.Add(busyGracePeriod).Add(timeout)
		status.IdleDeadlineAt = &deadline
		status.State = machinediagnostics.WorkspaceBusy
		status.BlocksMachineShutdown = true
		status.BlockerReason = "Workspace is busy"
		return evaluatedWorkspace{status: status, workspace: workspace}
	}
	deadline := activity.Add(timeout)
	status.IdleDeadlineAt = &deadline
	if deadline.After(now) {
		status.State = machinediagnostics.WorkspaceActive
		status.BlocksMachineShutdown = true
		status.BlockerReason = "Waiting for inactivity deadline"
	} else {
		status.State = machinediagnostics.WorkspaceIdleDue
	}
	return evaluatedWorkspace{status: status, workspace: workspace}
}

func blockedWorkspaceStatus(id string, state machinediagnostics.WorkspaceEvaluationState, reason string) machinediagnostics.WorkspaceStatus {
	return machinediagnostics.WorkspaceStatus{ID: id, State: state, BlocksMachineShutdown: true, BlockerReason: reason}
}

func diagnosticsWorkspaceStatuses(configs []string) []machinediagnostics.WorkspaceStatus {
	return evaluateMachineInactivity(configs, activityHeartbeat(), "", time.Now()).statuses
}

func workspaceIDFromConfig(path string) string {
	parent := filepath.Dir(path)
	if filepath.Base(parent) == "agent" {
		parent = filepath.Dir(parent)
	}
	return filepath.Base(parent)
}
