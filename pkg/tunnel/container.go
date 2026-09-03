// Package tunnel provides the functions used by the CLI to tunnel into a container using either
// a tunneled connection from the workspace client (using a machine provider) or a direct SSH connection
// from the proxy client (Ssh, k8s or docker provider)
package tunnel

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/agent"
	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
	devssh "github.com/devsy-org/devsy/pkg/ssh"
	"github.com/devsy-org/devsy/pkg/transport"
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

	conn, err := transport.OpenCallbackConn(
		ctx,
		func(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
			return c.runHostTunnel(ctx, stdin, stdout, timeout)
		},
		transport.CallbackConnOptions{
			LocalAddr:  transport.NewAddr("workspace"),
			RemoteAddr: transport.NewAddr("provider:" + c.client.Provider()),
		},
	)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	return transport.RunManaged(transport.RunManagedOptions{
		Parent:        ctx,
		Conn:          conn,
		TransportSide: transport.SideProvider,
		Metadata: transport.LogMetadata{
			Provider: c.client.Provider(), Mode: workspaceSubcommand,
			Workspace: c.client.Workspace(), TransportImpl: "callback",
		},
		Handler: func(ctx context.Context) error {
			sshClient, err := devssh.ClientFromConn(conn, "", nil)
			if err != nil {
				return fmt.Errorf("create ssh client: %w", err)
			}
			defer log.Debugf("connection to container closed")
			log.Debugf("connected to host")
			updateCtx, cancelUpdate := context.WithCancel(ctx)
			var updateWG sync.WaitGroup
			if c.updateConfigInterval > 0 {
				updateWG.Go(func() {
					c.updateConfig(updateCtx, sshClient)
				})
			}
			defer stopUpdateThenClose(cancelUpdate, &updateWG, sshClient)
			if err := c.runInContainer(ctx, sshClient, handler, envVars); err != nil {
				return fmt.Errorf("run in container: %w", err)
			}
			return nil
		},
	})
}

// stopUpdateThenClose cancels the periodic config-update goroutine and waits
// for it to fully exit before closing sshClient. Closing sshClient before the
// goroutine returns would race its in-flight use of the same connection.
func stopUpdateThenClose(
	cancelUpdate context.CancelFunc,
	updateWG *sync.WaitGroup,
	sshClient io.Closer,
) {
	cancelUpdate()
	updateWG.Wait()
	_ = sshClient.Close()
}

// runHostTunnel injects the devsy agent onto the host and starts the SSH server,
// forwarding stdio through the provided pipes.
func (c *ContainerTunnel) runHostTunnel(
	ctx context.Context,
	stdinReader io.Reader, stdoutWriter io.Writer,
	timeout time.Duration,
) error {
	writer, done := log.PipeJSONStreamWithFallback(log.PassthroughWriter())
	defer func() {
		_ = writer.Close()
		<-done
	}()
	defer log.Debugf("Tunnel to host closed")

	command := fmt.Sprintf("'%s' internal ssh-server --stdio", c.client.AgentPath())
	if log.DebugEnabled() {
		command += " --debug"
	}
	return agent.InjectAgent(ctx, &agent.InjectOptions{
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

			err := c.client.RefreshOptions(ctx, nil, false)
			if err != nil {
				log.Errorf("Error refreshing workspace options: %v", err)
				break
			}

			workspaceInfo, agentInfo, err := c.client.AgentInfo(provider.CLIOptions{})
			if err != nil {
				log.Errorf("Error compressing workspace info: %v", err)
				break
			}

			buf := &bytes.Buffer{}
			command := fmt.Sprintf(
				"%q internal agent workspace update-config --workspace-info %q",
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

	writer, writerDone := log.PipeJSONStream()
	defer func() {
		_ = writer.Close()
		<-writerDone
	}()

	command := fmt.Sprintf(
		"%q internal agent container-tunnel --workspace-info %q",
		c.client.AgentPath(),
		workspaceInfo,
	)
	if log.DebugEnabled() {
		command += " --debug"
	}
	containerConn, err := devssh.OpenSessionConn(ctx, sshClient, devssh.SessionConnOptions{
		Command: command,
		Env:     envVars,
		Stderr:  writer,
	})
	if err != nil {
		return fmt.Errorf("open container transport: %w", err)
	}
	defer func() { _ = containerConn.Close() }()
	return transport.RunManaged(transport.RunManagedOptions{
		Parent:        ctx,
		Conn:          containerConn,
		TransportSide: transport.SideProvider,
		Metadata: transport.LogMetadata{
			Mode:          workspaceSubcommand,
			TransportImpl: "ssh_session",
		},
		Handler: func(ctx context.Context) error {
			containerClient, err := devssh.ClientFromConn(containerConn, "", nil)
			if err != nil {
				return fmt.Errorf("ssh client: %w", err)
			}
			defer func() { _ = containerClient.Close() }()
			log.Debugf("connected to container")
			return handler(ctx, containerClient)
		},
	})
}
