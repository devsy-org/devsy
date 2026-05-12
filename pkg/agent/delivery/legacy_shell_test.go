package delivery

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLegacyShellDelivery_Phase(t *testing.T) {
	d := &LegacyShellDelivery{}
	assert.Equal(t, PhasePostStart, d.Phase())
}

func TestLegacyShellDelivery_DeliverPreStart_ReturnsError(t *testing.T) {
	d := &LegacyShellDelivery{}
	err := d.DeliverPreStart(context.Background(), PreStartOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support pre-start")
}

func TestLegacyShellDelivery_DeliverPostStart_RequiresExecFunc(t *testing.T) {
	d := &LegacyShellDelivery{}
	err := d.DeliverPostStart(context.Background(), PostStartOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exec function is required")
}

func TestLegacyShellDelivery_Cleanup_IsNoOp(t *testing.T) {
	d := &LegacyShellDelivery{}
	err := d.Cleanup(context.Background(), "workspace-123")
	assert.NoError(t, err)
}

func TestLegacyShellDelivery_DownloadURL_Default(t *testing.T) {
	d := &LegacyShellDelivery{}
	url := d.downloadURL()
	assert.NotEmpty(t, url)
	assert.Contains(t, url, "github.com")
}

func TestLegacyShellDelivery_DownloadURL_Custom(t *testing.T) {
	d := &LegacyShellDelivery{DownloadURL: "https://custom.example.com/agent"}
	assert.Equal(t, "https://custom.example.com/agent", d.downloadURL())
}

func TestExecFuncFromDriver(t *testing.T) {
	var capturedUser, capturedCmd string
	cmdFn := func(ctx context.Context, user, command string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
		capturedUser = user
		capturedCmd = command
		return nil
	}

	execFn := ExecFuncFromDriver(cmdFn, "root")
	err := execFn(context.Background(), "echo hello", nil, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "root", capturedUser)
	assert.Equal(t, "echo hello", capturedCmd)
}
