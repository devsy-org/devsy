package sshtunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	client2 "github.com/devsy-org/devsy/pkg/client"
	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
	devssh "github.com/devsy-org/devsy/pkg/ssh"
	devsshagent "github.com/devsy-org/devsy/pkg/ssh/agent"
	"github.com/devsy-org/devsy/pkg/transport"
	"github.com/devsy-org/devsy/pkg/tunnel"
	"golang.org/x/crypto/ssh"
	"k8s.io/apimachinery/pkg/util/wait"
)

type (
	AgentInjectFunc  func(context.Context, string, io.Reader, io.Writer, io.WriteCloser) error
	TunnelServerFunc func(ctx context.Context, stdin io.WriteCloser, stdout io.Reader) (*config2.Result, error)
)

type ExecuteCommandOptions struct {
	Client           client2.WorkspaceClient
	AddPrivateKeys   bool
	AgentInject      AgentInjectFunc
	SSHCommand       string
	Command          string
	TunnelServerFunc TunnelServerFunc
}

// ExecuteCommand runs the command in an SSH Tunnel and returns the result.
// The SSH transport is managed as a connection; grpcBridge remains a bounded
// duplex operation between the SSH command and the gRPC server.
func ExecuteCommand(ctx context.Context, opts ExecuteCommandOptions) (*config2.Result, error) {
	log.Debugf("starting SSH tunnel execution: ssh=%q workspace=%q addKeys=%v",
		opts.SSHCommand, opts.Command, opts.AddPrivateKeys)

	grpcBridge, err := tunnel.NewPipeBridge()
	if err != nil {
		return nil, err
	}
	defer grpcBridge.Close()

	var result *config2.Result

	conn, err := transport.OpenCallbackConn(ctx, func(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
		return executeSSHServerHelper(ctx, opts, stdin, stdout)
	}, transport.CallbackConnOptions{
		LocalAddr:  transport.NewAddr("devcontainer"),
		RemoteAddr: transport.NewAddr("ssh-server"),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	if opts.AddPrivateKeys {
		addPrivateKeys(ctx)
	}
	result, err = runSSHTunnel(ctx, sshTunnelParams{
		opts: opts, conn: conn, grpcBridge: grpcBridge,
	})

	return result, err
}

func isExpectedError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr != nil && !exitErr.Exited()
	}
	return false
}

func executeSSHServerHelper(
	ctx context.Context,
	opts ExecuteCommandOptions,
	stdin io.Reader,
	stdout io.Writer,
) error {
	defer log.Debug("done executing SSH server helper command")

	// AgentInject's stderr carries the ssh-server command's structured
	// JSON logs, and for some callers (see newAgentInjectFunc) also the
	// injection script's plain-text preamble.
	streamer := newSSHTunnelJSONLogStreamer()
	defer func() { _ = streamer.Close() }()

	log.Debugf("injecting and running SSH server command: %q", opts.SSHCommand)
	err := opts.AgentInject(ctx, opts.SSHCommand, stdin, stdout, streamer)
	if err != nil && !isExpectedError(err) {
		return fmt.Errorf("executing agent command: %w", err)
	}
	return nil
}

func addPrivateKeys(ctx context.Context) {
	log.Debug("adding SSH keys to agent")
	err := devssh.AddPrivateKeysToAgent(ctx)
	if err != nil {
		log.Debugf("failed to add private keys to SSH agent: %v", err)
	}
}

type sshTunnelParams struct {
	opts       ExecuteCommandOptions
	conn       transport.ManagedConn
	grpcBridge *tunnel.PipeBridge
}

func runSSHTunnel(ctx context.Context, p sshTunnelParams) (*config2.Result, error) {
	start := time.Now()
	log.Infof("tunnel: setup start")
	defer func() { log.Infof("tunnel: setup complete elapsed=%s", time.Since(start)) }()

	log.Debug("creating SSH client")
	sshClient, err := devssh.ClientFromConn(p.conn, "", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create SSH client: %w", err)
	}
	log.Debugf("tunnel: ssh client created elapsed=%s", time.Since(start))
	defer func() {
		_ = sshClient.Close()
		log.Debug("SSH client closed")
	}()

	sess, err := establishSSHSession(ctx, sshClient)
	if err != nil {
		return nil, err
	}
	log.Debugf("tunnel: ssh session established elapsed=%s", time.Since(start))
	defer func() {
		_ = sess.Close()
		log.Debug("SSH session closed")
	}()

	setupSSHAgentForwarding(sshClient, sess)

	var result *config2.Result

	err = p.grpcBridge.RunPair(ctx,
		func(ctx context.Context, stdin, stdout *os.File) error {
			return runCommandInSSHTunnel(ctx, sshCommandParams{
				sshClient: sshClient,
				command:   p.opts.Command,
			}, stdin, stdout)
		},
		func(ctx context.Context, stdout, stdin *os.File) error {
			var serverErr error
			result, serverErr = p.opts.TunnelServerFunc(ctx, stdin, stdout)
			return serverErr
		},
	)

	return result, err
}

func establishSSHSession(
	ctx context.Context,
	sshClient *ssh.Client,
) (*ssh.Session, error) {
	backoff := wait.Backoff{
		Duration: 500 * time.Millisecond,
		Factor:   1.5,
		Jitter:   0.1,
		Steps:    20,
	}

	var session *ssh.Session
	if err := wait.ExponentialBackoffWithContext(
		ctx,
		backoff,
		func(ctx context.Context) (bool, error) {
			sess, err := sshClient.NewSession()
			if err != nil {
				log.Debugf("SSH server not ready: %v", err)
				return false, nil // Retry
			}
			log.Debug("SSH session created")
			session = sess
			return true, nil // Success
		},
	); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, fmt.Errorf("SSH server timeout: %w", err)
	}

	return session, nil
}

// setupSSHAgentForwarding configures SSH agent forwarding on the session.
//
// Failures are logged but never fatal. This matches OpenSSH's behavior:
//   - clientloop.c: client_request_agent() returns NULL on failure,
//     sending SSH2_MSG_CHANNEL_OPEN_FAILURE without terminating the session.
//   - ssh_config(5) ExitOnForwardFailure only covers "dynamic, tunnel,
//     local, and remote port forwardings" — agent forwarding is excluded.
//
// Stale SSH_AUTH_SOCK is common in practice (tmux, screen, reconnected
// terminals), so a fatal error here would break devsy up for many users.
func setupSSHAgentForwarding(sshClient *ssh.Client, sess *ssh.Session) {
	identityAgent := devsshagent.GetSSHAuthSocket()
	if identityAgent == "" {
		return
	}

	log.Debugf("forwarding SSH agent: socket=%s", identityAgent)

	var err error
	if err = devsshagent.ForwardToRemote(sshClient, identityAgent); err == nil {
		err = devsshagent.RequestAgentForwarding(sess)
	}

	if err != nil {
		log.Warnf("SSH agent forwarding failed (continuing without agent): %v", err)
	}
}

type sshCommandParams struct {
	sshClient *ssh.Client
	command   string
}

func runCommandInSSHTunnel(ctx context.Context, p sshCommandParams, stdin, stdout *os.File) error {
	streamer := newSSHTunnelJSONLogStreamer()
	defer func() { _ = streamer.Close() }()

	log.Debugf("running agent command in SSH tunnel: %q", p.command)
	err := devssh.Run(ctx, devssh.RunOptions{
		Client:  p.sshClient,
		Command: p.command,
		Stdin:   stdin,
		Stdout:  stdout,
		Stderr:  streamer,
	})
	if err != nil {
		_ = streamer.Close()
		if out := streamer.ErrorOutput(); out != "" {
			return fmt.Errorf("run agent command failed: %w\n%s", err, out)
		}
		return fmt.Errorf("run agent command failed: %w", err)
	}
	return nil
}

const maxLogLines = 1

func newSSHTunnelJSONLogStreamer() *log.JSONLogStreamer {
	return log.NewJSONLogStreamer(log.StreamerOptions{
		FallbackLevel:           log.LevelDebug,
		CaptureLines:            maxLogLines,
		DetectLevelPrefixes:     true,
		TreatUnknownJSONAsDebug: true,
	})
}
