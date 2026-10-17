package workspace

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	devcconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"golang.org/x/crypto/ssh"
)

const testExecCat = "cat"

type tunnelExecSuite struct{ suite.Suite }

func TestTunnelExecSuite(t *testing.T) { suite.Run(t, new(tunnelExecSuite)) }

type tunnelWorkspaceClient struct {
	client.WorkspaceClient
	workspace *provider.Workspace
	info      provider.ProviderAgentConfig
	state     client.Status
	statusErr error
}

func (c *tunnelWorkspaceClient) WorkspaceConfig() *provider.Workspace { return c.workspace }
func (c *tunnelWorkspaceClient) Workspace() string                    { return c.workspace.ID }

func (c *tunnelWorkspaceClient) AgentInfo(
	provider.CLIOptions,
) (string, *provider.AgentWorkspaceInfo, error) {
	return "", &provider.AgentWorkspaceInfo{Agent: c.info}, nil
}

func (c *tunnelWorkspaceClient) Status(
	context.Context,
	client.StatusOptions,
) (client.Status, error) {
	return c.state, c.statusErr
}

func (s *tunnelExecSuite) TestRoutesAgentRuntimesWithoutLocalExecutable() {
	for _, driver := range []string{provider.ExternalDriver, provider.MicrosandboxDriver} {
		s.Run(driver, func() {
			c := &tunnelWorkspaceClient{
				workspace: &provider.Workspace{ID: "remote"},
				info:      provider.ProviderAgentConfig{Driver: driver, Local: "false"},
			}
			resolved, err := NewWorkspaceRuntime(&config.Config{}, c, "")
			s.Require().NoError(err)
			s.IsType(&tunnelRuntime{}, resolved)
			_, err = NewWorkspaceRuntime(&config.Config{}, c, "docker")
			s.ErrorContains(err, "cannot override")
		})
	}
	c := &tunnelWorkspaceClient{
		workspace: &provider.Workspace{},
		info:      provider.ProviderAgentConfig{},
	}
	resolved, err := NewWorkspaceRuntime(&config.Config{}, c, "")
	s.Require().NoError(err)
	s.IsType(&DockerRuntime{}, resolved)
}

func (s *tunnelExecSuite) TestFindRequiresRunningOwnedWorkspaceAndMetadata() {
	s.T().Setenv(config.EnvHome, s.T().TempDir())
	config.ResetPathManager()
	s.T().Cleanup(config.ResetPathManager)
	ws := &provider.Workspace{ID: "exec-fixture", Context: config.DefaultContext}
	s.Require().NoError(provider.SaveWorkspaceConfig(ws))
	c := &tunnelWorkspaceClient{workspace: ws, state: client.StatusStopped}
	r := &tunnelRuntime{client: c}
	_, err := r.FindRunning(s.T().Context(), ws.ID, nil)
	s.ErrorContains(err, "not running")
	c.state = client.StatusRunning
	_, err = r.FindRunning(s.T().Context(), "different-workspace", nil)
	s.ErrorContains(err, "does not match")
	_, err = r.FindRunning(s.T().Context(), ws.ID, nil)
	s.ErrorContains(err, "no saved container result")
	details := &devcconfig.ContainerDetails{ID: "native-vm"}
	details.Config.Labels = map[string]string{"team": "owned"}
	s.Require().
		NoError(provider.SaveWorkspaceResult(ws, &devcconfig.Result{ContainerDetails: details}))
	found, err := r.FindRunning(s.T().Context(), ws.ID, []string{"team=owned"})
	s.Require().NoError(err)
	s.Equal(details, found)
	_, err = r.FindRunning(s.T().Context(), ws.ID, []string{"team=other"})
	s.ErrorContains(err, "does not match label")
	_, err = r.FindRunning(s.T().Context(), ws.ID, []string{"missing="})
	s.ErrorContains(err, "does not match label")
	c.statusErr = errors.New("provider status unavailable")
	_, err = r.FindRunning(s.T().Context(), ws.ID, nil)
	s.ErrorIs(err, c.statusErr)
}

func (s *tunnelExecSuite) TestCommandPreservesLiteralArgvStreamsEnvironmentAndDirectory() {
	if runtime.GOOS == execTestWindowsOS {
		s.T().Skip("POSIX guest shell contract")
	}
	dir := s.T().TempDir()
	req := ExecRequest{
		Workdir: dir,
		Env:     map[string]string{"EXEC_VALUE": "literal $HOME; 'value'"},
		Argv: []string{
			"sh",
			"-c",
			`printf '%s\n' "$PWD" "$EXEC_VALUE" "$1" "$2" "$3"; cat; printf 'guest stderr' >&2; exit 17`,
			"sh",
			"space value",
			"",
			"$(touch should-not-exist); quote ' and \"",
		},
	}
	command, err := tunnelExecCommand(req)
	s.Require().NoError(err)
	// #nosec G204 -- test-owned directory and fixed literal argv exercise guest shell quoting
	cmd := exec.CommandContext(s.T().Context(), "sh", "-c", command)
	cmd.Stdin = bytes.NewBufferString("guest stdin")
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err = cmd.Run()
	var exit *exec.ExitError
	s.Require().ErrorAs(err, &exit)
	s.Equal(17, exit.ExitCode())
	s.Equal(
		dir+"\nliteral $HOME; 'value'\nspace value\n\n$(touch should-not-exist); quote ' and \"\nguest stdin",
		out.String(),
	)
	s.Equal("guest stderr", stderr.String())
	s.NoFileExists(dir + "/should-not-exist")
	req.Target.User = "developer"
	userCommand, err := tunnelExecCommand(req)
	s.Require().NoError(err)
	s.Contains(userCommand, "su -s /bin/sh -c")
	s.Contains(userCommand, "developer")
	_, err = tunnelExecCommand(ExecRequest{})
	s.ErrorContains(err, "nonempty executable")
}

func (s *tunnelExecSuite) TestSSHSessionPreservesGuestExitStatus() {
	for _, code := range []uint32{0, 17, 129, 130, 143} {
		s.Run(fmt.Sprint(code), func() {
			session := execTestSSHSession(s.T(), func(channel ssh.Channel) {
				_, _ = channel.SendRequest(
					"exit-status",
					false,
					ssh.Marshal(struct{ Status uint32 }{code}),
				)
			})
			err := runTunnelExecSession(s.T().Context(), session, ExecRequest{}, "guest-command")
			if code == 0 {
				s.NoError(err)
				return
			}
			var exit *ssh.ExitError
			s.Require().ErrorAs(err, &exit)
			s.Equal(int(code), exit.ExitStatus())
		})
	}
}

func (s *tunnelExecSuite) TestSSHTransportFailureDoesNotEchoEnvironmentValues() {
	session := execTestSSHSession(s.T(), func(ssh.Channel) {})
	command, err := tunnelExecCommand(
		ExecRequest{
			Env:  map[string]string{"PRIVATE_VALUE": "remote-only-canary-14928"},
			Argv: []string{testExecCat},
		},
	)
	s.Require().NoError(err)
	err = runTunnelExecSession(s.T().Context(), session, ExecRequest{}, command)
	s.Require().Error(err)
	s.NotContains(err.Error(), "remote-only-canary-14928")
	s.NotContains(err.Error(), command)
}

func execTestSSHSession(t *testing.T, finish func(ssh.Channel)) *ssh.Session {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	_, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(key)
	require.NoError(t, err)
	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(signer)
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		serveExecTestSSHConn(conn, cfg, finish)
	}()
	client, err := ssh.Dial("tcp", listener.Addr().String(), &ssh.ClientConfig{
		User:            "test",
		HostKeyCallback: ssh.FixedHostKey(signer.PublicKey()),
		Timeout:         5 * time.Second,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = client.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("SSH fixture failed to stop")
		}
	})
	session, err := client.NewSession()
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func (s *tunnelExecSuite) TestAlreadyCanceledSessionDoesNotDispatchCommand() {
	var dispatched atomic.Bool
	session := execTestSSHSession(s.T(), func(ssh.Channel) { dispatched.Store(true) })
	ctx, cancel := context.WithCancel(s.T().Context())
	cancel()
	err := runTunnelExecSession(ctx, session, ExecRequest{}, "must-not-run")
	s.ErrorIs(err, context.Canceled)
	s.False(dispatched.Load())
}

func serveExecTestSSHConn(conn net.Conn, cfg *ssh.ServerConfig, finish func(ssh.Channel)) {
	server, channels, requests, serverErr := ssh.NewServerConn(conn, cfg)
	if serverErr != nil {
		return
	}
	defer func() { _ = server.Close() }()
	go ssh.DiscardRequests(requests)
	for incoming := range channels {
		channel, requests, channelErr := incoming.Accept()
		if channelErr != nil {
			return
		}
		for request := range requests {
			if request.Type != "exec" {
				_ = request.Reply(false, nil)
				continue
			}
			_ = request.Reply(true, nil)
			finish(channel)
			_ = channel.Close()
			break
		}
	}
}
