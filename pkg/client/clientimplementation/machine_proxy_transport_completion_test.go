package clientimplementation

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/transport"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/stretchr/testify/require"
)

const testProviderCreateCommand = "create"

func TestMachineCommandTransportCompletesWithoutClosingInput(t *testing.T) {
	payload := strings.Repeat("m", 128*1024)
	const stderrTail = "machine stderr tail"
	command := "printf '%s' '" + payload + "'; printf '%s' '" + stderrTail + "' >&2"
	cfg := &provider.ProviderConfig{
		Name: testCancelProvider,
		Exec: provider.ProviderCommands{
			Create:       types.StrArray{testProviderCreateCommand},
			Command:      types.StrArray{command},
			Cancellation: provider.ProviderCancellationFile,
		},
	}
	devsy := &config.Config{
		DefaultContext: testLocksContext,
		Contexts:       map[string]*config.ContextConfig{testLocksContext: {}},
	}
	machine, err := NewMachineClient(
		devsy,
		cfg,
		&provider.Machine{ID: "completion-machine", Context: testLocksContext},
	)
	require.NoError(t, err)
	var stderr bytes.Buffer
	conn, err := machine.OpenCommandTransport(context.Background(), client.CommandTransportOptions{
		Command: testOpaqueCommand,
		Stderr:  &stderr,
	})
	require.NoError(t, err)
	requireTransportCompletionWithOpenInput(t, conn, payload)
	require.Equal(t, stderrTail, stderr.String())
}

func TestProxySSHTransportCompletesWithoutClosingInput(t *testing.T) {
	payload := strings.Repeat("p", 128*1024)
	workspace := workspaceCancellationTestClient()
	workspace.providerConfig.Exec.Proxy = &provider.ProxyCommands{
		Ssh: types.StrArray{"printf '%s' '" + payload + "'"},
	}
	proxy, err := NewProxyClient(
		workspace.devsyConfig,
		workspace.providerConfig,
		workspace.providerWorkspace,
	)
	require.NoError(t, err)
	conn, err := proxy.OpenSSHTransport(context.Background(), client.SSHTransportOptions{})
	require.NoError(t, err)
	requireTransportCompletionWithOpenInput(t, conn, payload)
}

func requireTransportCompletionWithOpenInput(
	t *testing.T,
	conn transport.ManagedConn,
	payload string,
) {
	t.Helper()
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	got := make([]byte, len(payload))
	_, err := io.ReadFull(conn, got)
	require.NoError(t, err)
	require.Equal(t, payload, string(got))
	finished := make(chan error, 1)
	go func() { finished <- conn.Wait() }()
	select {
	case err := <-finished:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("provider completion waited for caller to close transport input")
	}
	var trailing [1]byte
	_, err = conn.Read(trailing[:])
	require.ErrorIs(t, err, io.EOF)
}
