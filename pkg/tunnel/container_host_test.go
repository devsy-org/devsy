//go:build unix

package tunnel

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/shell"
	devssh "github.com/devsy-org/devsy/pkg/ssh"
	hostssh "github.com/devsy-org/ssh"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

const hostTunnelStdioFlag = "--stdio"

type localHostTunnelClient struct {
	client.WorkspaceClient
	path      string
	info      string
	dataPath  string
	refresh   func(context.Context) error
	pathCalls *atomic.Int32
}

func (c localHostTunnelClient) AgentPath() string {
	if c.pathCalls != nil {
		c.pathCalls.Add(1)
	}
	return c.path
}
func (localHostTunnelClient) AgentURL() string { return "" }
func (localHostTunnelClient) AgentLocal() bool { return true }

func (c localHostTunnelClient) AgentInfo(
	provider.CLIOptions,
) (string, *provider.AgentWorkspaceInfo, error) {
	return c.info, &provider.AgentWorkspaceInfo{
		Agent: provider.ProviderAgentConfig{DataPath: c.dataPath},
	}, nil
}

func (c localHostTunnelClient) RefreshOptions(ctx context.Context, _ []string, _ bool) error {
	return c.refresh(ctx)
}

func TestHostTunnelExecutesLiteralAgentPath(t *testing.T) {
	for _, name := range []string{"agent space", "agent'quote", "agent '\\$-\u03bb;$(false)"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, name)
			events := filepath.Join(dir, "events")
			stderr := filepath.Join(dir, "stderr")
			t.Setenv("DEVSY_TEST_HOST_TUNNEL_EVENTS", events)
			t.Setenv("DEVSY_TEST_HOST_TUNNEL_STDERR", stderr)
			t.Setenv("DEVSY_TEST_HOST_TUNNEL_LEVEL", log.LevelString())
			script := `#!/bin/sh
exec 2>>"$DEVSY_TEST_HOST_TUNNEL_STDERR"
printf 'child_started\n' >>"$DEVSY_TEST_HOST_TUNNEL_EVENTS"
if [ "$#" -eq 5 ] && [ "$1" = internal ] && [ "$2" = ssh-server ] && [ "$3" = --stdio ] &&
   [ "$4" = --log-level ] && [ "$5" = "$DEVSY_TEST_HOST_TUNNEL_LEVEL" ]; then
  printf 'command_admitted\n' >>"$DEVSY_TEST_HOST_TUNNEL_EVENTS"
else
  printf 'command_mismatch\n' >>"$DEVSY_TEST_HOST_TUNNEL_EVENTS"
fi
printf '%s\000' "$0" "$@"
printf 'child_stdout_written\n' >>"$DEVSY_TEST_HOST_TUNNEL_EVENTS"
cat
result=$?
printf 'child_completed\n' >>"$DEVSY_TEST_HOST_TUNNEL_EVENTS"
exit "$result"
`
			writeHostAgent(t, path, script)
			var stdout bytes.Buffer
			var pathCalls atomic.Int32
			tunnel := NewContainerTunnel(localHostTunnelClient{path: path, pathCalls: &pathCalls})
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			err := tunnel.runHostTunnel(
				ctx,
				strings.NewReader("host-input\n"),
				&stdout,
				time.Second,
			)
			if err != nil {
				t.Fatalf(
					"host tunnel failed: %v; path lookups=%d; output bytes=%d; events=%q; child stderr=%q",
					err,
					pathCalls.Load(),
					stdout.Len(),
					readHostFixtureDiagnostic(events),
					readHostFixtureDiagnostic(stderr),
				)
			}
			phases := readHostFixtureDiagnostic(events)
			require.Contains(t, phases, "command_admitted\n")
			require.Contains(t, phases, "child_stdout_written\n")
			require.Contains(t, phases, "child_completed\n")
			args := []string{path, "internal", "ssh-server", hostTunnelStdioFlag}
			args = append(args, log.ForwardFlagArgs()...)
			require.Equal(t, strings.Join(args, "\x00")+"\x00host-input\n", stdout.String())
		})
	}
}

func readHostFixtureDiagnostic(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return "<unavailable>"
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, 4096))
	if err != nil {
		return "<unavailable>"
	}
	return string(data)
}

func TestContainerTunnelPreservesLiteralHostArguments(t *testing.T) {
	for _, mode := range []string{"container-tunnel", "update-config"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "agent '$HOME-\\\u03bb")
			record := filepath.Join(dir, "arguments")
			t.Setenv("HOST_TUNNEL_RECORD", record)
			root := writeHostAgent(
				t,
				path,
				"#!/bin/sh\nprintf '%s\\000' \"$0\" \"$@\" > \"$HOST_TUNNEL_RECORD\"\n",
			)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			workspace := localHostTunnelClient{
				path:     path,
				info:     "workspace-info",
				dataPath: filepath.Join(dir, "data $HOME"),
			}
			calls := 0
			workspace.refresh = func(ctx context.Context) error {
				calls++
				if calls > 1 {
					cancel()
				}
				return ctx.Err()
			}
			tunnel := NewContainerTunnel(workspace)
			client := localHostShellSSH(t)
			args := []string{path, "internal", "agent"}
			if mode == "update-config" {
				tunnel.updateConfigInterval = time.Millisecond
				tunnel.updateConfig(ctx, client)
				args = append(
					args,
					"workspace",
					mode,
					"--workspace-info",
					workspace.info,
					"--agent-dir",
					workspace.dataPath,
				)
				args = append(args, log.ForwardFlagArgs()...)
			} else {
				_ = tunnel.runInContainer(ctx, client, func(context.Context, *ssh.Client) error {
					t.Error("fixture does not serve an inner SSH connection")
					return nil
				}, nil)
				args = append(args, mode, "--workspace-info", workspace.info)
				args = append(args, log.ForwardFlagArgs()...)
			}
			got, err := root.ReadFile("arguments")
			require.NoError(t, err)
			require.Equal(t, strings.Join(args, "\x00")+"\x00", string(got))
		})
	}
}

func writeHostAgent(t *testing.T, path, script string) *os.Root {
	t.Helper()
	root, err := os.OpenRoot(filepath.Dir(path))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, root.Close()) })
	name := filepath.Base(path)
	require.NoError(t, root.WriteFile(name, []byte(script), 0o600))
	require.NoError(t, root.Chmod(name, 0o700))
	return root
}

func localHostShellSSH(t *testing.T) *ssh.Client {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(key)
	require.NoError(t, err)
	server := &hostssh.Server{Handler: func(session hostssh.Session) {
		err := shell.RunEmulatedShell(session.Context(), &shell.CommandRunner{
			Command: session.RawCommand(),
			Stdin:   session,
			Stdout:  session,
			Stderr:  session.Stderr(),
		})
		if err != nil {
			_ = session.Exit(1)
			return
		}
		_ = session.Exit(0)
	}}
	server.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		_ = server.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("host SSH fixture did not stop")
		}
	})
	conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	client, err := devssh.ClientFromConn(conn, "fixture", nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}
