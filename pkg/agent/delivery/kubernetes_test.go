package delivery

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKubernetesDelivery_Phase(t *testing.T) {
	d := &KubernetesDelivery{}
	assert.Equal(t, PhasePostStart, d.Phase())
}

func TestKubernetesDelivery_DeliverPreStart_ReturnsError(t *testing.T) {
	d := &KubernetesDelivery{}
	err := d.DeliverPreStart(context.Background(), PreStartOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support pre-start")
}

func TestKubernetesDelivery_DeliverPostStart_RequiresBinarySource(t *testing.T) {
	d := &KubernetesDelivery{
		ExecFunc: func(_ context.Context, _ string, _ io.Reader, _ io.Writer, _ io.Writer) error {
			return nil
		},
	}
	err := d.DeliverPostStart(context.Background(), PostStartOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "binary source is required")
}

func TestKubernetesDelivery_DeliverPostStart_RequiresExecFunc(t *testing.T) {
	d := &KubernetesDelivery{}
	err := d.DeliverPostStart(context.Background(), PostStartOptions{
		BinarySource: fakeBinarySource,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exec function is required")
}

func TestKubernetesDelivery_DeliverPostStart_WritesBinary(t *testing.T) {
	binaryData := "test-binary-content"
	var capturedCmd string
	var capturedStdin bytes.Buffer

	execFn := func(_ context.Context, cmd string, stdin io.Reader, _ io.Writer, _ io.Writer) error {
		capturedCmd = cmd
		if stdin != nil {
			_, _ = io.Copy(&capturedStdin, stdin)
		}
		return nil
	}

	d := &KubernetesDelivery{ExecFunc: execFn}
	err := d.DeliverPostStart(context.Background(), PostStartOptions{
		BinarySource: func(_ context.Context, _ string) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(binaryData)), nil
		},
		Arch: "amd64",
	})

	require.NoError(t, err)

	destPath := agent.ContainerDevsyHelperLocation
	assert.Contains(t, capturedCmd, "gzip -d")
	assert.Contains(t, capturedCmd, destPath)
	assert.Contains(t, capturedCmd, "chmod 755")

	gr, err := gzip.NewReader(&capturedStdin)
	require.NoError(t, err)
	decompressed, err := io.ReadAll(gr)
	require.NoError(t, err)
	assert.Equal(t, binaryData, string(decompressed))
}

func TestKubernetesDelivery_DeliverPostStart_BinarySourceError(t *testing.T) {
	execFn := func(_ context.Context, _ string, _ io.Reader, _ io.Writer, _ io.Writer) error {
		return nil
	}

	d := &KubernetesDelivery{ExecFunc: execFn}
	err := d.DeliverPostStart(context.Background(), PostStartOptions{
		BinarySource: func(_ context.Context, _ string) (io.ReadCloser, error) {
			return nil, fmt.Errorf("download failed")
		},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "acquire binary")
}

func TestKubernetesDelivery_DeliverPostStart_ExecError(t *testing.T) {
	execFn := func(_ context.Context, _ string, _ io.Reader, _ io.Writer, _ io.Writer) error {
		return fmt.Errorf("exec failed")
	}

	d := &KubernetesDelivery{ExecFunc: execFn}
	err := d.DeliverPostStart(context.Background(), PostStartOptions{
		BinarySource: fakeBinarySource,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "write binary to container")
}

func TestKubernetesDelivery_Cleanup_IsNoOp(t *testing.T) {
	d := &KubernetesDelivery{}
	err := d.Cleanup(context.Background(), "workspace-123")
	assert.NoError(t, err)
}
