package cmdinternal

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/cmd/internal/agentworkspace"
	"github.com/devsy-org/devsy/pkg/agent"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/encoding"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/log"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/status"
	"github.com/spf13/cobra"
)

// containerRootUser is the user to use when running commands inside the container that
// require root privileges.
const containerRootUser = "root"

// sighupExitGrace is the grace period after SIGHUP before os.Exit(0) is
// called, to avoid orphaning any processes that ignore context cancellation.
const sighupExitGrace = 5 * time.Second

var findDevContainerTimeout = 30 * time.Second

// ContainerTunnelCmd holds the container-tunnel cmd flags.
type ContainerTunnelCmd struct {
	*flags.GlobalFlags

	WorkspaceInfo string
	User          string
}

// NewContainerTunnelCmd creates the container-tunnel command, which brings a
// workspace's devcontainer up if needed and then bridges an SSH tunnel into
// it over the calling process's stdio.
func NewContainerTunnelCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &ContainerTunnelCmd{
		GlobalFlags: globalFlags,
	}
	containerTunnelCmd := &cobra.Command{
		Use:   "container-tunnel",
		Short: "Starts a new container ssh tunnel",
		Args:  cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, _ []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}

	cliflags.Add(containerTunnelCmd,
		cliflags.String(&cmd.User, names.User, "", "The user to create the tunnel with"),
		cliflags.String(&cmd.WorkspaceInfo, names.WorkspaceInfo, "", "The workspace info"),
	)
	_ = containerTunnelCmd.MarkFlagRequired(names.WorkspaceInfo)
	return containerTunnelCmd
}

// Run brings the workspace's devcontainer to a running state and then
// blocks bridging an SSH tunnel into it, until the tunnel closes or cobraCtx
// is cancelled.
func (cmd *ContainerTunnelCmd) Run(cobraCtx context.Context) error {
	ctx, cancel := context.WithCancel(cobraCtx)
	defer cancel()

	stopSighupWatch := watchForSighup(cancel)
	defer stopSighupWatch()

	shouldExit, workspaceInfo, err := agent.WriteWorkspaceInfo(cmd.WorkspaceInfo)
	if err != nil {
		return fmt.Errorf("write workspace info: %w", err)
	}
	if shouldExit {
		return nil
	}

	if _, err := agentworkspace.InitContentFolder(ctx, workspaceInfo); err != nil {
		return fmt.Errorf("init content folder: %w", err)
	}

	runner, err := agentworkspace.CreateRunner(ctx, workspaceInfo)
	if err != nil {
		return fmt.Errorf("create runner: %w", err)
	}

	if err := startDevContainer(ctx, workspaceInfo, runner); err != nil {
		return err
	}

	return agent.Tunnel(ctx, agent.TunnelOptions{
		Exec: func(
			ctx context.Context,
			user, command string,
			stdin io.Reader,
			stdout, stderr io.Writer,
		) error {
			return runner.Command(ctx, devcontainer.CommandParams{
				User:    user,
				Command: command,
				Stdin:   stdin,
				Stdout:  stdout,
				Stderr:  stderr,
			})
		},
		User:            cmd.User,
		Stdin:           os.Stdin,
		Stdout:          os.Stdout,
		Stderr:          os.Stderr,
		Timeout:         workspaceInfo.InjectTimeout,
		RemoteAgentPath: workspaceInfo.Agent.ContainerInstallPath(),
		DownloadURL:     workspaceInfo.Agent.DownloadURL,
	})
}

// watchForSighup cancels on SIGHUP (the SSH channel driving this process
// closed) so in-flight commands are killed instead of orphaned, then falls
// back to os.Exit(0) if something still ignores cancellation.
func watchForSighup(cancel context.CancelFunc) (stop func()) {
	done := make(chan struct{})
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGHUP)

	go func() {
		select {
		case <-done:
			return
		case <-sigs:
			cancel()
		}
		select {
		case <-done:
		case <-time.After(sighupExitGrace):
			os.Exit(0)
		}
	}()

	return func() {
		signal.Stop(sigs)
		close(done)
	}
}

// startDevContainer ensures workspaceConfig's devcontainer is up and, for
// legacy UIDs, that its result file is readable, (re)starting the container
// when either check fails.
func startDevContainer(
	ctx context.Context,
	workspaceConfig *provider2.AgentWorkspaceInfo,
	runner devcontainer.Runner,
) error {
	findCtx, cancel := context.WithTimeout(ctx, findDevContainerTimeout)
	defer cancel()

	containerDetails, err := runner.Find(findCtx)
	if err != nil {
		return fmt.Errorf("find devcontainer: %w", err)
	}

	if containerDetails == nil || containerDetails.State.Status != config.ContainerStatusRunning {
		if err := startContainer(ctx, runner, workspaceConfig); err != nil {
			return fmt.Errorf("start container: %w", err)
		}
		return nil
	}

	if encoding.IsLegacyUID(workspaceConfig.Workspace.UID) &&
		!hasDevContainerResult(ctx, runner) {
		if err := startContainer(ctx, runner, workspaceConfig); err != nil {
			return fmt.Errorf("restart container after missing devcontainer result: %w", err)
		}
	}

	return nil
}

// hasDevContainerResult reports whether the devcontainer result file is
// readable inside the running container.
func hasDevContainerResult(ctx context.Context, runner devcontainer.Runner) bool {
	var buf bytes.Buffer
	err := runner.Command(ctx, devcontainer.CommandParams{
		User:    containerRootUser,
		Command: pkgconfig.ReadDevContainerResultCommand(),
		Stdout:  &buf,
		Stderr:  &buf,
	})
	return err == nil
}

// startContainer runs the devcontainer's full up workflow, skipping the
// image build, to bring the container to a running state.
func startContainer(
	ctx context.Context,
	runner devcontainer.Runner,
	workspaceConfig *provider2.AgentWorkspaceInfo,
) error {
	log.Debug("starting container")
	if _, err := runner.Up(
		ctx,
		devcontainer.UpOptions{NoBuild: true},
		workspaceConfig.InjectTimeout,
		status.Nop(),
	); err != nil {
		return fmt.Errorf("up devcontainer: %w", err)
	}
	log.Debug("started container")
	return nil
}
