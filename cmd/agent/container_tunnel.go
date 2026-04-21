package agent

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/devsy-org/devsy/cmd/agent/workspace"
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/agent"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/encoding"
	"github.com/devsy-org/devsy/pkg/log"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/spf13/cobra"
)

// ContainerTunnelCmd holds the ws-tunnel cmd flags.
type ContainerTunnelCmd struct {
	*flags.GlobalFlags

	WorkspaceInfo string
	User          string
}

// NewContainerTunnelCmd creates a new command.
func NewContainerTunnelCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &ContainerTunnelCmd{
		GlobalFlags: flags,
	}
	containerTunnelCmd := &cobra.Command{
		Use:   "container-tunnel",
		Short: "Starts a new container ssh tunnel",
		Args:  cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, _ []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}

	containerTunnelCmd.Flags().
		StringVar(&cmd.User, "user", "", "The user to create the tunnel with")
	containerTunnelCmd.Flags().
		StringVar(&cmd.WorkspaceInfo, "workspace-info", "", "The workspace info")
	_ = containerTunnelCmd.MarkFlagRequired("workspace-info")
	return containerTunnelCmd
}

// Run runs the command logic.
func (cmd *ContainerTunnelCmd) Run(ctx context.Context) error {
	// write workspace info
	shouldExit, workspaceInfo, err := agent.WriteWorkspaceInfo(cmd.WorkspaceInfo)
	if err != nil {
		return err
	} else if shouldExit {
		return nil
	}

	// make sure content folder exists
	_, err = workspace.InitContentFolder(workspaceInfo)
	if err != nil {
		return err
	}

	// create runner
	runner, err := workspace.CreateRunner(workspaceInfo)
	if err != nil {
		return err
	}

	// wait until devcontainer is started
	err = startDevContainer(ctx, workspaceInfo, runner)
	if err != nil {
		return err
	}

	// handle SIGHUP
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGHUP)
	go func() {
		<-sigs
		os.Exit(0)
	}()

	// create tunnel into container.
	err = agent.Tunnel(
		ctx,
		func(ctx context.Context, user string, command string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
			return runner.Command(ctx, user, command, stdin, stdout, stderr)
		},
		cmd.User,
		os.Stdin,
		os.Stdout,
		os.Stderr,
		workspaceInfo.InjectTimeout,
	)
	if err != nil {
		return err
	}

	return nil
}

func startDevContainer(
	ctx context.Context,
	workspaceConfig *provider2.AgentWorkspaceInfo,
	runner devcontainer.Runner,
) error {
	containerDetails, err := runner.Find(ctx)
	if err != nil {
		return err
	}

	// start container if necessary
	if containerDetails == nil || containerDetails.State.Status != "running" {
		// start container
		_, err = StartContainer(ctx, runner, workspaceConfig)
		if err != nil {
			return err
		}
	} else if encoding.IsLegacyUID(workspaceConfig.Workspace.UID) {
		// make sure workspace result is in devcontainer
		buf := &bytes.Buffer{}
		err = runner.Command(ctx, "root", "cat "+pkgconfig.DevContainerResultPath, nil, buf, buf)
		if err != nil {
			// start container
			_, err = StartContainer(ctx, runner, workspaceConfig)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func StartContainer(
	ctx context.Context,
	runner devcontainer.Runner,
	workspaceConfig *provider2.AgentWorkspaceInfo,
) (*config.Result, error) {
	log.Debugf("starting Devsy container")
	result, err := runner.Up(
		ctx,
		devcontainer.UpOptions{NoBuild: true},
		workspaceConfig.InjectTimeout,
	)
	if err != nil {
		return result, err
	}
	log.Debugf("started Devsy container")
	return result, err
}
