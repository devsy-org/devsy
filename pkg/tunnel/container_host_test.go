//go:build unix

package tunnel

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"net"
	"os"
	"path/filepath"
	"strings"
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
	path     string
	info     string
	dataPath string
	refresh  func(context.Context) error
}

func (c localHostTunnelClient) AgentPath() string { return c.path }
func (localHostTunnelClient) AgentURL() string    { return "" }
func (localHostTunnelClient) AgentLocal() bool    { return true }

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
			path := filepath.Join(t.TempDir(), name)
			writeHostAgent(t, path, "#!/bin/sh\nprintf '%s\\000' \"$0\" \"$@\"\ncat\n")
			var stdout bytes.Buffer
			tunnel := NewContainerTunnel(localHostTunnelClient{path: path})
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			require.NoError(
				t,
				tunnel.runHostTunnel(ctx, strings.NewReader("host-input\n"), &stdout, time.Second),
			)
			args := []string{path, "internal", "ssh-server", hostTunnelStdioFlag}
			if log.DebugEnabled() {
				args = append(args, "--debug")
			}
			require.Equal(t, strings.Join(args, "\x00")+"\x00host-input\n", stdout.String())
		})
	}
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
			} else {
				_ = tunnel.runInContainer(ctx, client, func(context.Context, *ssh.Client) error {
					t.Error("fixture does not serve an inner SSH connection")
					return nil
				}, nil)
				args = append(args, mode, "--workspace-info", workspace.info)
				if log.DebugEnabled() {
					args = append(args, "--debug")
				}
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
