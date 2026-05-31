// Package tunnel provides the functions used by the CLI to tunnel into a container using either
// a tunneled connection from the workspace client (using a machine provider) or a direct SSH connection
// from the proxy client (Ssh, k8s or docker provider)
package tunnel

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/devsy-org/devsy/pkg/agent"
	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
	devssh "github.com/devsy-org/devsy/pkg/ssh"
	"golang.org/x/crypto/ssh"
)

// ContainerTunnel manages the state of the tunnel to the container.
type ContainerTunnel struct {
	client               client.WorkspaceClient
	updateConfigInterval time.Duration
}

// NewContainerTunnel constructs a ContainerTunnel using the workspace client, if proxy is True then
// the workspace's agent config is not periodically updated.
func NewContainerTunnel(client client.WorkspaceClient) *ContainerTunnel {
	updateConfigInterval := time.Second * 30
	return &ContainerTunnel{
		client:               client,
		updateConfigInterval: updateConfigInterval,
	}
}

// Handler defines what to do once the tunnel has a client established.
type Handler func(ctx context.Context, containerClient *ssh.Client) error

// Run creates an "outer" tunnel to the host to start the SSH server so that the "inner" tunnel can
// connect to the container over SSH.
func (c *ContainerTunnel) Run(
	ctx context.Context,
	handler Handler,
	cfg *config.Config,
	envVars map[string]string,
) error {
	if handler == nil {
		return nil
	}

	timeout := config.ParseTimeOption(cfg, config.ContextOptionAgentInjectTimeout)

	pb, err := NewPipeBridge()
	if err != nil {
		return err
	}
	defer pb.Close()

	return pb.RunPair(ctx,
		func(ctx context.Context, stdin, stdout *os.File) error {
			return c.runHostTunnel(ctx, stdin, stdout, timeout)
		},
		func(ctx context.Context, stdout, stdin *os.File) error {
			sshClient, err := devssh.StdioClient(stdout, stdin, false)
			if err != nil {
				return fmt.Errorf("create ssh client: %w", err)
			}
			defer func() { _ = sshClient.Close() }()
			defer log.Debugf("connection to container closed")
			log.Debugf("connected to host")

			if c.updateConfigInterval > 0 {
				go c.updateConfig(ctx, sshClient)
			}

			if err := c.runInContainer(ctx, sshClient, handler, envVars); err != nil {
				return fmt.Errorf("run in container: %w", err)
			}
			return nil
		},
	)
}

// runHostTunnel injects the devsy agent onto the host and starts the SSH server,
// forwarding stdio through the provided pipes.
func (c *ContainerTunnel) runHostTunnel(
	ctx context.Context,
	stdinReader, stdoutWriter *os.File,
	timeout time.Duration,
) error {
	writer := log.Writer(log.LevelInfo)
	defer func() { _ = writer.Close() }()
	defer log.Debugf("Tunnel to host closed")

	command := fmt.Sprintf("%q internal helper ssh-server --stdio", c.client.AgentPath())
	if log.DebugEnabled() {
		command += " --debug"
	}
	return agent.InjectAgent(&agent.InjectOptions{
		Ctx: ctx,
		Exec: func(ctx context.Context, command string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
			return c.client.Command(ctx, client.CommandOptions{
				Command: command,
				Stdin:   stdin,
				Stdout:  stdout,
				Stderr:  stderr,
			})
		},
		IsLocal:         c.client.AgentLocal(),
		RemoteAgentPath: c.client.AgentPath(),
		DownloadURL:     c.client.AgentURL(),
		Command:         command,
		Stdin:           stdinReader,
		Stdout:          stdoutWriter,
		Stderr:          writer,
		Timeout:         timeout,
	})
}

// updateConfig is called periodically to keep the workspace agent config up to date.
func (c *ContainerTunnel) updateConfig(ctx context.Context, sshClient *ssh.Client) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(c.updateConfigInterval):
			log.Debugf("Start refresh")

			// update options
			err := c.client.RefreshOptions(ctx, nil, false)
			if err != nil {
				log.Errorf("Error refreshing workspace options: %v", err)
				break
			}

			// compress info
			workspaceInfo, agentInfo, err := c.client.AgentInfo(provider.CLIOptions{})
			if err != nil {
				log.Errorf("Error compressing workspace info: %v", err)
				break
			}

			// update workspace remotely
			buf := &bytes.Buffer{}
			command := fmt.Sprintf(
				"%s%q internal agent workspace update-config --workspace-info %q",
				agent.ContainerAgentEnvPrefix,
				c.client.AgentPath(),
				workspaceInfo,
			)
			if agentInfo.Agent.DataPath != "" {
				command += fmt.Sprintf(" --agent-dir %q", agentInfo.Agent.DataPath)
			}

			log.Debugf("Run command in container: %s", command)
			err = devssh.Run(ctx, devssh.RunOptions{
				Client:  sshClient,
				Command: command,
				Stdout:  buf,
				Stderr:  buf,
			})
			if err != nil {
				log.Errorf("Error updating remote workspace: %s%v", buf.String(), err)
			} else {
				log.Debugf("Out: %s", buf.String())
			}
		}
	}
}

// runInContainer uses the connected SSH client to execute handler on the remote.
func (c *ContainerTunnel) runInContainer(
	ctx context.Context,
	sshClient *ssh.Client,
	handler Handler,
	envVars map[string]string,
) error {
	workspaceInfo, _, err := c.client.AgentInfo(provider.CLIOptions{})
	if err != nil {
		return err
	}

	pb, err := NewPipeBridge()
	if err != nil {
		return err
	}
	defer pb.Close()

	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// tunnel to container
	tunnelDone := make(chan error, 1)
	go func() {
		tunnelDone <- c.runContainerTunnel(cancelCtx, containerTunnelOpts{
			sshClient:     sshClient,
			workspaceInfo: workspaceInfo,
			stdinReader:   pb.StdinReader,
			stdoutWriter:  pb.StdoutWriter,
			envVars:       envVars,
		})
	}()

	containerClient, err := devssh.StdioClient(pb.StdoutReader, pb.StdinWriter, false)
	if err != nil {
		// StdioClient failed — check if the tunnel goroutine already exited
		// with an error. If so, the tunnel error is the root cause.
		select {
		case tunnelErr := <-tunnelDone:
			if tunnelErr != nil {
				return tunnelErr
			}
		default:
		}
		return fmt.Errorf("ssh client: %w", err)
	}
	defer func() { _ = containerClient.Close() }()
	log.Debugf("connected to container")

	return handler(cancelCtx, containerClient)
}

type containerTunnelOpts struct {
	sshClient     *ssh.Client
	workspaceInfo string
	stdinReader   *os.File
	stdoutWriter  *os.File
	envVars       map[string]string
}

// runContainerTunnel runs the container tunnel SSH command. It closes
// stdoutWriter on exit so StdioClient gets EOF when the tunnel dies.
// Context-cancelled errors are suppressed (expected during normal shutdown).
func (c *ContainerTunnel) runContainerTunnel(ctx context.Context, opts containerTunnelOpts) error {
	writer := log.Writer(log.LevelInfo)
	defer func() { _ = writer.Close() }()
	defer func() { _ = opts.stdoutWriter.Close() }()

	log.Debugf("Run container tunnel")
	defer log.Debugf("Container tunnel exited")

	command := fmt.Sprintf(
		"%s%q internal agent container-tunnel --workspace-info %q",
		agent.ContainerAgentEnvPrefix,
		c.client.AgentPath(),
		opts.workspaceInfo,
	)
	if log.DebugEnabled() {
		command += " --debug"
	}
	err := devssh.Run(ctx, devssh.RunOptions{
		Client:  opts.sshClient,
		Command: command,
		Stdin:   opts.stdinReader,
		Stdout:  opts.stdoutWriter,
		Stderr:  writer,
		EnvVars: opts.envVars,
	})
	if err != nil && ctx.Err() == nil {
		return fmt.Errorf("container tunnel: %w", err)
	}
	return nil
}
