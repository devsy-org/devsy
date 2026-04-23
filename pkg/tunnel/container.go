// Package tunnel provides the functions used by the CLI to tunnel into a container using either
// a tunneled connection from the workspace client (using a machine provider) or a direct SSH connection
// from the proxy client (Ssh, k8s or docker provider)
package tunnel

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/agent"
	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
	devssh "github.com/devsy-org/devsy/pkg/ssh"
	"golang.org/x/crypto/ssh"
)

// NewContainerTunnel constructs a ContainerTunnel using the workspace client, if proxy is True then
// the workspace's agent config is not periodically updated.
//
//nolint:funcorder
func NewContainerTunnel(client client.WorkspaceClient) *ContainerTunnel {
	updateConfigInterval := time.Second * 30
	return &ContainerTunnel{
		client:               client,
		updateConfigInterval: updateConfigInterval,
	}
}

// ContainerTunnel manages the state of the tunnel to the container.
type ContainerTunnel struct {
	client               client.WorkspaceClient
	updateConfigInterval time.Duration
}

// Handler defines what to do once the tunnel has a client established.
type Handler func(ctx context.Context, containerClient *ssh.Client) error

// Run creates an "outer" tunnel to the host to start the SSH server so that the "inner" tunnel can
// connect to the container over SSH.
//
//nolint:funlen // goroutine setup with InjectAgent options is inherently verbose
func (c *ContainerTunnel) Run(
	ctx context.Context,
	handler Handler,
	cfg *config.Config,
	envVars map[string]string,
) error {
	if handler == nil {
		return nil
	}

	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		return err
	}
	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		return err
	}
	defer func() { _ = stdoutWriter.Close() }()
	defer func() { _ = stdinWriter.Close() }()

	timeout := config.ParseTimeOption(cfg, config.ContextOptionAgentInjectTimeout)

	// tunnel to host
	tunnelChan := make(chan error, 1)
	go func() {
		writer := log.Writer(log.LevelInfo)
		defer func() { _ = writer.Close() }()
		defer log.Debugf("Tunnel to host closed")

		command := fmt.Sprintf("'%s' helper ssh-server --stdio", c.client.AgentPath())
		if log.DebugEnabled() {
			command += " --debug"
		}
		tunnelChan <- agent.InjectAgent(&agent.InjectOptions{
			Ctx: cancelCtx,
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
	}()

	// connect to container
	containerChan := make(chan error, 1)
	go func() {
		sshClient, err := devssh.StdioClient(stdoutReader, stdinWriter, false)
		if err != nil {
			containerChan <- fmt.Errorf("create ssh client: %w", err)
			return
		}

		defer func() { _ = sshClient.Close() }()
		defer cancel()
		defer log.Debugf("connection to container closed")
		log.Debugf("connected to host")

		if c.updateConfigInterval > 0 {
			go func() {
				c.updateConfig(cancelCtx, sshClient)
			}()
		}

		if err := c.runInContainer(cancelCtx, sshClient, handler, envVars); err != nil {
			containerChan <- fmt.Errorf("run in container: %w", err)
		} else {
			containerChan <- nil
		}
	}()

	return awaitGoroutines(tunnelChan, containerChan, stdoutWriter, stdinWriter)
}

// awaitGoroutines waits for both the container and tunnel goroutines to report
// and returns the appropriate error. The container result is the primary result;
// the tunnel result provides root-cause context when the container gets EOF.
func awaitGoroutines(
	tunnelChan, containerChan <-chan error,
	stdoutWriter, stdinWriter *os.File,
) error {
	var tunnelErr, containerErr error

	select {
	case containerErr = <-containerChan:
		select {
		case tunnelErr = <-tunnelChan:
		default:
		}
	case tunnelErr = <-tunnelChan:
		// Host tunnel exited before container finished. Close pipes to unblock
		// the container goroutine (may be blocked in SSH handshake).
		_ = stdoutWriter.Close()
		_ = stdinWriter.Close()
		containerErr = <-containerChan
	}

	return classifyTunnelErrors(tunnelErr, containerErr)
}

// classifyTunnelErrors determines which error to report when the tunnel and/or
// container goroutines fail. EOF errors from the container are suppressed when
// the tunnel error is the root cause.
func classifyTunnelErrors(tunnelErr, containerErr error) error {
	if containerErr == nil {
		return nil
	}
	if isEOFError(containerErr) {
		if tunnelErr != nil {
			return fmt.Errorf("connect to server: %w", tunnelErr)
		}
		return nil
	}
	return fmt.Errorf("tunnel to container: %w", containerErr)
}

// isEOFError reports whether an error is caused by an EOF condition,
// including wrapped SSH handshake failures from closed pipes.
func isEOFError(err error) bool {
	return errors.Is(err, io.EOF) || strings.Contains(err.Error(), ": EOF")
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
				"'%s' agent workspace update-config --workspace-info '%s'",
				c.client.AgentPath(),
				workspaceInfo,
			)
			if agentInfo.Agent.DataPath != "" {
				command += fmt.Sprintf(" --agent-dir '%s'", agentInfo.Agent.DataPath)
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

	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		return err
	}
	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		return err
	}
	defer func() { _ = stdoutWriter.Close() }()
	defer func() { _ = stdinWriter.Close() }()

	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// tunnel to container
	tunnelDone := make(chan error, 1)
	go func() {
		tunnelDone <- c.runContainerTunnel(
			cancelCtx, sshClient, workspaceInfo, stdinReader, stdoutWriter, envVars,
		)
	}()

	containerClient, err := devssh.StdioClient(stdoutReader, stdinWriter, false)
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

// runContainerTunnel runs the container tunnel SSH command. It closes
// stdoutWriter on exit so StdioClient gets EOF when the tunnel dies.
// Context-cancelled errors are suppressed (expected during normal shutdown).
//
//nolint:revive // argument-limit: pipe endpoints require separate reader/writer files
func (c *ContainerTunnel) runContainerTunnel(
	ctx context.Context,
	sshClient *ssh.Client,
	workspaceInfo string,
	stdinReader, stdoutWriter *os.File,
	envVars map[string]string,
) error {
	writer := log.Writer(log.LevelInfo)
	defer func() { _ = writer.Close() }()
	defer func() { _ = stdoutWriter.Close() }()

	log.Debugf("Run container tunnel")
	defer log.Debugf("Container tunnel exited")

	command := fmt.Sprintf(
		"'%s' agent container-tunnel --workspace-info '%s'",
		c.client.AgentPath(),
		workspaceInfo,
	)
	if log.DebugEnabled() {
		command += " --debug"
	}
	err := devssh.Run(ctx, devssh.RunOptions{
		Client:  sshClient,
		Command: command,
		Stdin:   stdinReader,
		Stdout:  stdoutWriter,
		Stderr:  writer,
		EnvVars: envVars,
	})
	if err != nil && ctx.Err() == nil {
		return fmt.Errorf("container tunnel: %w", err)
	}
	return nil
}
