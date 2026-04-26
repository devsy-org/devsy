package sshtunnel

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	client2 "github.com/devsy-org/devsy/pkg/client"
	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
	devssh "github.com/devsy-org/devsy/pkg/ssh"
	devsshagent "github.com/devsy-org/devsy/pkg/ssh/agent"
	"github.com/devsy-org/devsy/pkg/tunnel"
	"golang.org/x/crypto/ssh"
	"k8s.io/apimachinery/pkg/util/wait"
)

type (
	AgentInjectFunc  func(context.Context, string, *os.File, *os.File, io.WriteCloser) error
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
// It uses two PipeBridges: sshBridge connects the helper (agent inject) to
// the SSH tunnel, and grpcBridge connects the SSH command to the gRPC server.
func ExecuteCommand(ctx context.Context, opts ExecuteCommandOptions) (*config2.Result, error) {
	log.Debugf("starting SSH tunnel execution: ssh=%q workspace=%q addKeys=%v",
		opts.SSHCommand, opts.Command, opts.AddPrivateKeys)

	sshBridge, err := tunnel.NewPipeBridge()
	if err != nil {
		return nil, err
	}
	defer sshBridge.Close()

	grpcBridge, err := tunnel.NewPipeBridge()
	if err != nil {
		return nil, err
	}
	defer grpcBridge.Close()

	var result *config2.Result

	err = sshBridge.RunPair(ctx,
		func(ctx context.Context, stdin, stdout *os.File) error {
			return executeSSHServerHelper(ctx, opts, stdin, stdout)
		},
		func(ctx context.Context, stdout, stdin *os.File) error {
			if opts.AddPrivateKeys {
				addPrivateKeys(ctx)
			}
			var runErr error
			result, runErr = runSSHTunnel(ctx, sshTunnelParams{
				opts:       opts,
				stdout:     stdout,
				stdin:      stdin,
				grpcBridge: grpcBridge,
			})
			return runErr
		},
	)

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
	stdin, stdout *os.File,
) error {
	defer log.Debug("done executing SSH server helper command")

	writer := log.Writer(log.LevelInfo)
	defer func() { _ = writer.Close() }()

	log.Debugf("injecting and running SSH server command: %q", opts.SSHCommand)
	err := opts.AgentInject(ctx, opts.SSHCommand, stdin, stdout, writer)
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
	stdout     *os.File
	stdin      *os.File
	grpcBridge *tunnel.PipeBridge
}

func runSSHTunnel(ctx context.Context, p sshTunnelParams) (*config2.Result, error) {
	start := time.Now()
	log.Infof("tunnel: setup start")
	defer func() { log.Infof("tunnel: setup complete elapsed=%s", time.Since(start)) }()

	log.Debug("creating SSH client")
	sshClient, err := devssh.StdioClient(p.stdout, p.stdin, false)
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
	streamer := NewTunnelLogStreamer()
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

type TunnelLogStreamer struct {
	pw   *io.PipeWriter
	done chan struct{}

	mu        sync.Mutex
	lastLines []string
}

func NewTunnelLogStreamer() *TunnelLogStreamer {
	pr, pw := io.Pipe()
	l := &TunnelLogStreamer{
		pw:        pw,
		done:      make(chan struct{}),
		lastLines: make([]string, 0, maxLogLines),
	}

	go l.process(pr)
	return l
}

func (l *TunnelLogStreamer) Write(p []byte) (int, error) {
	return l.pw.Write(p)
}

func (l *TunnelLogStreamer) Close() error {
	err := l.pw.Close()
	<-l.done
	return err
}

func (l *TunnelLogStreamer) ErrorOutput() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.lastLines) == 0 {
		return ""
	}

	return strings.Join(l.lastLines, "\n")
}

func (l *TunnelLogStreamer) process(r io.Reader) {
	defer close(l.done)
	scanner := bufio.NewScanner(r)

	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		l.logLine(line)

		l.mu.Lock()
		if len(l.lastLines) >= maxLogLines {
			l.lastLines = l.lastLines[1:]
		}
		l.lastLines = append(l.lastLines, line)
		l.mu.Unlock()
	}

	if err := scanner.Err(); err != nil {
		log.Debugf("error reading tunnel output: %v", err)
	}
}

type jsonLogLine struct {
	Message string `json:"message,omitempty"`
	Msg     string `json:"msg,omitempty"`
	Level   string `json:"level,omitempty"`
}

func (j *jsonLogLine) text() string {
	if j.Message != "" {
		return j.Message
	}
	return j.Msg
}

func (l *TunnelLogStreamer) logLine(line string) {
	line = strings.TrimSpace(line)
	// Remove carriage returns to prevent terminal overwriting (e.g. git progress)
	line = strings.ReplaceAll(line, "\r", "")
	if line == "" {
		return
	}

	var obj jsonLogLine
	if json.Unmarshal([]byte(line), &obj) == nil && obj.text() != "" {
		level := normalizeLevel(obj.Level)
		logAtLevel(level, obj.text())
		return
	}

	if matched, level := extractLogLevel(line); matched {
		logAtLevel(level, line)
	} else {
		log.Debug(line)
	}
}

const (
	levelDebug = "debug"
	levelInfo  = "info"
	levelWarn  = "warn"
	levelError = "error"
	levelFatal = "fatal"
)

func normalizeLevel(raw string) string {
	switch strings.ToLower(raw) {
	case "trace", levelDebug:
		return levelDebug
	case levelInfo:
		return levelInfo
	case "warning", levelWarn:
		return levelWarn
	case levelError, "panic", levelFatal:
		return levelError
	default:
		return levelDebug
	}
}

func extractLogLevel(line string) (bool, string) {
	parts := strings.SplitN(line, " ", 3)
	if len(parts) < 2 || !strings.Contains(parts[0], ":") {
		return false, ""
	}

	level := strings.ToLower(parts[1])
	switch level {
	case levelDebug, levelInfo, levelWarn, levelError, levelFatal:
		return true, level
	default:
		return false, ""
	}
}

func logAtLevel(level, msg string) {
	switch level {
	case levelDebug:
		log.Debug(msg)
	case levelInfo:
		log.Info(msg)
	case levelWarn:
		log.Warn(msg)
	case levelError:
		log.Error(msg)
	case levelFatal:
		log.Error(msg)
	default:
		log.Debug(msg)
	}
}
