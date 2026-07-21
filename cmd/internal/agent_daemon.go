package cmdinternal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/agent"
	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver/custom"
	"github.com/devsy-org/devsy/pkg/log"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/spf13/cobra"
)

const (
	defaultPatrolInterval = time.Minute
	busyGracePeriod       = 20 * time.Minute
)

type DaemonCmd struct {
	*flags.GlobalFlags

	Interval       string
	ShutdownAction string
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
	daemonCmd.Flags().
		StringVar(&cmd.Interval, "interval", "", "The interval how to poll workspaces")
	daemonCmd.Flags().
		StringVar(
			&cmd.ShutdownAction, "shutdown-action", "",
			"The shutdown action (none, stopContainer, or stopCompose)",
		)
	return daemonCmd
}

func (cmd *DaemonCmd) Run(ctx context.Context) error {
	// The daemon runs only container/machine-side; the host never invokes it.
	if agent.IsHostAgentInvocation(cmd.AgentDir) {
		return errors.New(
			"`devsy internal agent daemon` is only valid inside the workspace container or machine",
		)
	}

	logDir, err := agent.GetAgentDaemonLogDir(cmd.AgentDir)
	if err != nil {
		return err
	}

	log.Infof("starting Devsy daemon patrol at %s", logDir)
	cmd.patrol(ctx)
	return nil
}

func (cmd *DaemonCmd) patrol(ctx context.Context) {
	cmd.initialTouch()

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
	baseDir, err = agent.FindAgentHomeDir(cmd.AgentDir)
	if err != nil {
		return "", nil, err
	}
	pattern := filepath.Join(
		baseDir,
		"contexts",
		"*",
		"workspaces",
		"*",
		provider2.WorkspaceConfigFile,
	)
	configs, err = filepath.Glob(pattern)
	if err != nil {
		return "", nil, fmt.Errorf("glob %s: %w", pattern, err)
	}
	return baseDir, configs, nil
}

func (cmd *DaemonCmd) patrolOnce(ctx context.Context) {
	baseDir, configs, err := cmd.workspaceConfigs()
	if err != nil {
		log.Errorf("list workspace configs: %v", err)
		return
	}

	latestActivity, workspace := findLatestActivity(configs)
	if latestActivity == nil {
		if len(configs) == 0 {
			log.Infof("no workspaces found in %q", baseDir)
		} else {
			log.Infof(
				"%d workspaces found in %q, but none had auto-stop configured or were still running",
				len(configs),
				baseDir,
			)
		}
		return
	}

	cmd.checkAndShutdown(ctx, *latestActivity, workspace)
}

func (cmd *DaemonCmd) checkAndShutdown(
	ctx context.Context,
	latestActivity time.Time,
	workspace *provider2.AgentWorkspaceInfo,
) {
	if cmd.ShutdownAction == config.ShutdownActionNone {
		return
	}

	timeout := agent.DefaultInactivityTimeout
	if workspace.Agent.Timeout != "" {
		parsed, err := time.ParseDuration(workspace.Agent.Timeout)
		if err != nil {
			log.Errorf("parse inactivity timeout, using %s: %v", timeout, err)
		} else {
			timeout = parsed
		}
	}

	deadline := latestActivity.Add(timeout)
	if deadline.After(time.Now()) {
		log.Infof(
			"workspace %q last active %s, auto-stop in %s",
			workspace.Workspace.ID,
			latestActivity.Format(time.RFC3339),
			time.Until(deadline).Round(time.Second),
		)
		return
	}

	cmd.runShutdownCommand(ctx, workspace)
}

func (cmd *DaemonCmd) runShutdownCommand(
	ctx context.Context,
	workspace *provider2.AgentWorkspaceInfo,
) {
	environ, err := custom.ToEnvironWithBinaries(ctx, workspace)
	if err != nil {
		log.Errorf("build shutdown environment: %v", err)
		return
	}

	shutdown := strings.Join(workspace.Agent.Exec.Shutdown, " ")
	log.Infof("running shutdown command for workspace %s: %s", workspace.Workspace.ID, shutdown)

	var stdout, stderr bytes.Buffer
	err = clientimplementation.RunCommand(clientimplementation.RunCommandOptions{
		Ctx:     ctx,
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
		return
	}

	log.Infof("ran shutdown command (stdout: %s, stderr: %s)", stdout.String(), stderr.String())
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
