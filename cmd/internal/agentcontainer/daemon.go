package agentcontainer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	config2 "github.com/devsy-org/devsy/pkg/config"
	agentd "github.com/devsy-org/devsy/pkg/daemon/agent"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/platform/client"
	"github.com/devsy-org/devsy/pkg/sharedfile"
	"github.com/devsy-org/devsy/pkg/ts"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

const RootDir = config2.ContainerDataDir

type DaemonCmd struct {
	Config *agentd.DaemonConfig
}

// NewDaemonCmd creates the merged daemon command.
func NewDaemonCmd() *cobra.Command {
	cmd := &DaemonCmd{
		Config: &agentd.DaemonConfig{},
	}
	daemonCmd := &cobra.Command{
		Use:   "daemon",
		Short: "Starts the Devsy network daemon, SSH server and monitors container activity if timeout is set",
		Args:  cobra.NoArgs,
		RunE:  cmd.Run,
	}
	cliflags.Add(
		daemonCmd,
		cliflags.String(
			&cmd.Config.Timeout,
			names.Timeout,
			"",
			"The timeout to stop the container after",
		),
		cliflags.String(
			&cmd.Config.ShutdownAction,
			names.ShutdownAction,
			"",
			"The shutdown action (none, stopContainer, or stopCompose)",
		),
	)
	return daemonCmd
}

func (cmd *DaemonCmd) Run(c *cobra.Command, args []string) error {
	if err := cmd.loadConfig(); err != nil {
		return err
	}

	timeoutDuration, err := cmd.setupTimeout()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(c.Context(), os.Interrupt, syscall.SIGTERM)

	g, ctx := errgroup.WithContext(ctx)
	if err := cmd.startDaemonTasks(ctx, g, timeoutDuration); err != nil {
		stop()
		return err
	}

	err = g.Wait()
	stop() // Restore default signal handling before exiting.
	os.Exit(daemonExitCode(err))
	return nil // Unreachable but needed.
}

// daemonExitCode maps the task group result to a process exit code. Inactivity
// expiry is the normal way a managed container stops, not a failure.
func daemonExitCode(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, agentd.ErrInactivityExpired):
		log.Infof("inactivity timeout reached, daemon exiting")
		return 0
	default:
		log.Errorf("daemon error: %v", err)
		return 1
	}
}

func (cmd *DaemonCmd) setupTimeout() (time.Duration, error) {
	if cmd.Config.Timeout == "" {
		return 0, nil
	}

	timeoutDuration, err := time.ParseDuration(cmd.Config.Timeout)
	if err != nil {
		return 0, fmt.Errorf("failed to parse timeout duration: %w", err)
	}
	if timeoutDuration > 0 {
		if err := sharedfile.EnsureMode(config2.ContainerActivityFile, 0o666); err != nil {
			return 0, fmt.Errorf("failed to ensure activity file: %w", err)
		}
	}

	return timeoutDuration, nil
}

func (cmd *DaemonCmd) startDaemonTasks(
	ctx context.Context,
	g *errgroup.Group,
	timeoutDuration time.Duration,
) error {
	var tasksStarted bool

	// Start process reaper only when this process really is the init process of
	// its PID namespace; otherwise it would steal other processes' children.
	if os.Getpid() == 1 {
		g.Go(func() error {
			agentd.RunProcessReaper(ctx)
			return nil
		})
	}

	// Start Tailscale networking server.
	if cmd.shouldRunNetworkServer() {
		tasksStarted = true
		g.Go(func() error {
			return runNetworkServer(ctx, cmd)
		})
	}

	loop, err := cmd.newLifecycleLoop(timeoutDuration)
	if err != nil {
		return err
	}
	if loop != nil {
		tasksStarted = true
		g.Go(func() error {
			return loop.Run(ctx)
		})
	}

	// Start ssh server.
	if cmd.shouldRunSsh() {
		tasksStarted = true
		g.Go(func() error {
			return runSshServer(ctx, cmd)
		})
	}

	// In case no task is configured, just wait indefinitely.
	if !tasksStarted {
		g.Go(func() error {
			<-ctx.Done()
			return nil
		})
	}
	return nil
}

// newLifecycleLoop builds the inactivity loop. A managed daemon starts unarmed
// and waits for setup; any other daemon with a timeout is armed immediately.
func (cmd *DaemonCmd) newLifecycleLoop(timeout time.Duration) (*agentd.LifecycleLoop, error) {
	monitor := &agentd.LifecycleMonitor{}
	loop := &agentd.LifecycleLoop{Monitor: monitor, ActivityPath: config2.ContainerActivityFile}
	enabled := timeout > 0 && cmd.Config.ShutdownAction != config.ShutdownActionNone

	if !cmd.Config.Lifecycle.Managed() {
		if !enabled {
			return nil, nil
		}
		monitor.ArmNow(timeout, time.Now())
		return loop, nil
	}

	owner, err := agentd.NewLifecycleOwner(config2.ContainerLifecycleDir)
	if err != nil {
		return nil, fmt.Errorf("publish container lifecycle owner: %w", err)
	}
	if err := sharedfile.EnsureMode(config2.ContainerActivityFile, 0o666); err != nil {
		_ = owner.Close()
		return nil, fmt.Errorf("ensure activity file: %w", err)
	}
	loop.Owner = owner
	if enabled && !cmd.Config.Lifecycle.SetupArmsTimer {
		monitor.ArmNow(timeout, time.Now())
	}
	return loop, nil
}

// loadConfig loads the daemon configuration from base64-encoded JSON.
// If a CLI-provided timeout exists, it will override the timeout in the config.
func (cmd *DaemonCmd) loadConfig() error {
	cfg, err := agentd.LoadContainerDaemonConfig()
	if err != nil {
		return err
	}
	if cfg == nil {
		return nil
	}
	if cmd.Config.Timeout != "" {
		cfg.Timeout = cmd.Config.Timeout
	}
	if cmd.Config.ShutdownAction != "" {
		cfg.ShutdownAction = cmd.Config.ShutdownAction
	}
	cmd.Config = cfg
	return nil
}

// shouldRunNetworkServer returns true if the required platform parameters are present.
func (cmd *DaemonCmd) shouldRunNetworkServer() bool {
	return cmd.Config.Platform.AccessKey != "" &&
		cmd.Config.Platform.PlatformHost != "" &&
		cmd.Config.Platform.WorkspaceHost != ""
}

// shouldRunSsh returns true if at least one SSH configuration value is provided.
func (cmd *DaemonCmd) shouldRunSsh() bool {
	return cmd.Config.Ssh.Workdir != "" || cmd.Config.Ssh.User != ""
}

// runNetworkServer starts the network server.
func runNetworkServer(
	ctx context.Context,
	cmd *DaemonCmd,
) error {
	if err := os.MkdirAll(RootDir, os.ModePerm); err != nil {
		return err
	}
	config := client.NewConfig()
	config.AccessKey = cmd.Config.Platform.AccessKey
	config.Host = "https://" + cmd.Config.Platform.PlatformHost
	config.Insecure = true
	baseClient := client.NewClientFromConfig(config)
	if err := baseClient.RefreshSelf(ctx); err != nil {
		return fmt.Errorf("failed to refresh client: %w", err)
	}
	tsServer := ts.NewWorkspaceServer(&ts.WorkspaceServerConfig{
		AccessKey:     cmd.Config.Platform.AccessKey,
		PlatformHost:  ts.RemoveProtocol(cmd.Config.Platform.PlatformHost),
		WorkspaceHost: cmd.Config.Platform.WorkspaceHost,
		RootDir:       RootDir,
		Insecure:      true,
		LogF: func(format string, args ...any) {
			log.Infof(format, args...)
		},
	})
	if err := tsServer.Start(ctx); err != nil {
		return fmt.Errorf("network server: %w", err)
	}
	return nil
}

// sshServerArgs builds the embedded SSH server command line. A managed daemon
// asks it to record connection activity so Platform and direct SSH routes keep
// the workspace alive.
func sshServerArgs(cfg *agentd.DaemonConfig) []string {
	args := []string{cmdInternal, cmdAgent, cmdContainer, "ssh-server"}
	if cfg.Ssh.Workdir != "" {
		args = append(args, names.Flag(names.Workdir), cfg.Ssh.Workdir)
	}
	if cfg.Ssh.User != "" {
		args = append(args, names.Flag(names.RemoteUser), cfg.Ssh.User)
	}
	if cfg.Lifecycle.Managed() {
		args = append(args, names.Flag(names.TrackActivity))
	}
	return args
}

// runSshServer starts the SSH server, sending SIGTERM on context cancellation
// with a grace period before force-killing.
func runSshServer(ctx context.Context, cmd *DaemonCmd) error {
	binaryPath, err := os.Executable()
	if err != nil {
		return err
	}

	args := sshServerArgs(cmd.Config)

	sshCmd := exec.CommandContext(ctx, binaryPath, args...) // #nosec G204
	sshCmd.Stdout = os.Stdout
	sshCmd.Stderr = os.Stderr
	sshCmd.Cancel = func() error {
		return sshCmd.Process.Signal(syscall.SIGTERM)
	}
	sshCmd.WaitDelay = 5 * time.Second // Graceful shutdown before force-killing.

	if err := sshCmd.Run(); err != nil && ctx.Err() == nil {
		return fmt.Errorf("SSH server exited abnormally: %w", err)
	}
	return nil
}
