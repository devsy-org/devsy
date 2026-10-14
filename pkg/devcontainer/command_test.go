package devcontainer

import (
	"bytes"
	"context"
	"testing"

	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/devsy-org/devsy/pkg/subprocess"
	"github.com/stretchr/testify/require"
)

type protocolCommandDriver struct {
	mockDriver
	output           *bytes.Buffer
	payload          []byte
	outputBeforeExit []byte
}

func (d *protocolCommandDriver) CommandDevContainer(
	_ context.Context,
	params *driver.CommandParams,
) error {
	redacted := &subprocess.StreamingRedactingWriter{
		Next: params.Stdout, Redactor: secrets.NewRedactor([]string{"TOKEN=private-canary-value"}),
	}
	output := params.Stdout
	if !params.RawStdout {
		output = redacted
	}
	// A peer must receive the complete packet before it can send its reply.
	if _, err := output.Write(d.payload); err != nil {
		return err
	}
	d.outputBeforeExit = bytes.Clone(d.output.Bytes())
	return redacted.Flush()
}

func TestCommandPreservesProtocolOutputBeforeExit(t *testing.T) {
	var output bytes.Buffer
	d := &protocolCommandDriver{output: &output, payload: []byte{0, 255, 'p'}}
	r := newTestRunner(d)
	require.NoError(
		t,
		r.Command(context.Background(), CommandParams{Stdout: &output, RawStdout: true}),
	)
	require.Equal(t, []byte{0, 255, 'p'}, d.outputBeforeExit,
		"buffering a secret prefix in a binary packet stalls the peer's next handshake step")
}

func TestCommandKeepsTextRedactionByDefault(t *testing.T) {
	var output bytes.Buffer
	d := &protocolCommandDriver{output: &output, payload: []byte("private-canary-value\n")}
	r := newTestRunner(d)
	require.NoError(t, r.Command(context.Background(), CommandParams{Stdout: &output}))
	require.Equal(t, "***\n", output.String())
}
