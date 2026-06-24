package delivery

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testVersion = "v1.2.3"

// recordingExec records each call, replays stdouts[N] as stdout, and returns
// errs[N] on call N (nil when unset).
type recordingExec struct {
	calls   []recordedCall
	stdouts []string
	errs    []error
}

type recordedCall struct {
	argv  []string
	stdin string
}

func (r *recordingExec) fn(_ context.Context, argv []string, streams driver.Streams) error {
	call := recordedCall{argv: argv}
	if streams.Stdin != nil {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, streams.Stdin)
		call.stdin = buf.String()
	}
	idx := len(r.calls)
	r.calls = append(r.calls, call)
	if streams.Stdout != nil && idx < len(r.stdouts) {
		_, _ = io.WriteString(streams.Stdout, r.stdouts[idx])
	}
	if idx < len(r.errs) {
		return r.errs[idx]
	}
	return nil
}

func binarySourceFrom(data string) BinarySourceFunc {
	return func(_ context.Context, _ string) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(data)), nil
	}
}

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
	exec := &recordingExec{}
	d := &KubernetesDelivery{Exec: exec.fn}
	err := d.DeliverPostStart(context.Background(), PostStartOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "binary source is required")
}

func TestKubernetesDelivery_DeliverPostStart_RequiresExec(t *testing.T) {
	d := &KubernetesDelivery{}
	err := d.DeliverPostStart(context.Background(), PostStartOptions{
		BinarySource: fakeBinarySource,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exec function is required")
}

func TestKubernetesDelivery_DeliverPostStart_WritesBinary(t *testing.T) {
	binaryData := "test-binary-content"
	// Probe returns nothing → deliver.
	exec := &recordingExec{stdouts: []string{""}}

	d := &KubernetesDelivery{Exec: exec.fn, ExpectedVersion: testVersion}
	err := d.DeliverPostStart(context.Background(), PostStartOptions{
		BinarySource: binarySourceFrom(binaryData),
		Arch:         testArch,
	})
	require.NoError(t, err)

	require.Len(t, exec.calls, 2)
	destPath := pkgconfig.ContainerDevsyHelperLocation

	probeScript := strings.Join(exec.calls[0].argv, " ")
	assert.Contains(t, probeScript, "--version")
	assert.Contains(t, probeScript, destPath)

	writeScript := strings.Join(exec.calls[1].argv, " ")
	assert.Contains(t, writeScript, "mktemp")
	assert.Contains(t, writeScript, "chmod 0755")
	assert.Contains(t, writeScript, "mv -f")
	assert.Contains(t, writeScript, destPath)
	assert.Equal(t, binaryData, exec.calls[1].stdin)
}

func TestKubernetesDelivery_DeliverPostStart_SkipsWhenVersionMatches(t *testing.T) {
	exec := &recordingExec{stdouts: []string{testVersion + "\n"}}

	d := &KubernetesDelivery{Exec: exec.fn, ExpectedVersion: testVersion}
	err := d.DeliverPostStart(context.Background(), PostStartOptions{
		BinarySource: binarySourceFrom("should-not-be-streamed"),
		Arch:         testArch,
	})
	require.NoError(t, err)

	require.Len(t, exec.calls, 1, "only the version probe should run")
	assert.Empty(t, exec.calls[0].stdin)
}

func TestKubernetesDelivery_DeliverPostStart_DeliversWhenProbeErrors(t *testing.T) {
	// A failing probe must not abort delivery; the write still succeeds.
	probeErr := &recordingExec{
		stdouts: []string{""},
		errs:    []error{fmt.Errorf("probe boom"), nil},
	}
	d := &KubernetesDelivery{Exec: probeErr.fn, ExpectedVersion: testVersion}

	err := d.DeliverPostStart(context.Background(), PostStartOptions{
		BinarySource: binarySourceFrom("data"),
		Arch:         testArch,
	})
	require.NoError(t, err)
	assert.Len(t, probeErr.calls, 2)
}

func TestKubernetesDelivery_DeliverPostStart_BinarySourceError(t *testing.T) {
	exec := &recordingExec{stdouts: []string{""}}
	d := &KubernetesDelivery{Exec: exec.fn}
	err := d.DeliverPostStart(context.Background(), PostStartOptions{
		BinarySource: func(_ context.Context, _ string) (io.ReadCloser, error) {
			return nil, fmt.Errorf("download failed")
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "acquire binary")
}

func TestKubernetesDelivery_Cleanup_IsNoOp(t *testing.T) {
	d := &KubernetesDelivery{}
	err := d.Cleanup(context.Background(), "workspace-123")
	assert.NoError(t, err)
}
