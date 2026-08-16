package tunnel

import (
	"context"
	"os"
	"time"

	"github.com/devsy-org/devsy/pkg/util/iojoin"
)

const joinTimeout = 5 * time.Second

// PipeBridge is a duplex bridge between two concurrent units of work that share
// resources. It is used to implement the bridge between the caller's stdio and
// the exec's stdio.
type PipeBridge struct {
	StdoutReader *os.File
	StdoutWriter *os.File
	StdinReader  *os.File
	StdinWriter  *os.File
}

// NewPipeBridge creates a bridge backed by two OS pipes. The caller owns the
// bridge and must Close it (RunPair does not).
func NewPipeBridge() (*PipeBridge, error) {
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
		return nil, err
	}
	return &PipeBridge{
		StdoutReader: stdoutReader,
		StdoutWriter: stdoutWriter,
		StdinReader:  stdinReader,
		StdinWriter:  stdinWriter,
	}, nil
}

// Close releases both pipe pairs. It is idempotent.
func (pb *PipeBridge) Close() {
	_ = pb.StdoutReader.Close()
	_ = pb.StdoutWriter.Close()
	_ = pb.StdinReader.Close()
	_ = pb.StdinWriter.Close()
}

type (
	// TunnelFunc is the "tunnel" side: it consumes the caller's input via stdin
	// and produces output via stdout (e.g. an SSH server run over the bridge).
	TunnelFunc func(ctx context.Context, stdin *os.File, stdout *os.File) error
	// HandlerFunc is the "handler" side: it consumes the tunnel's output via
	// stdout and produces input via stdin (e.g. the caller's stdio).
	HandlerFunc func(ctx context.Context, stdout *os.File, stdin *os.File) error
)

// RunPair runs the tunnel and handler sides of the bridge concurrently and
// returns once both have finished (or the slower side is abandoned after
// joinTimeout).
//
// Lifecycle:
//  1. Both sides run in their own goroutine under a shared cancellable context.
//  2. When either side returns, the other is signalled to stop: its context is
//     cancelled and the bridge write ends are closed so reads on the other
//     side observe EOF rather than blocking.
//  3. RunPair then waits for the remaining side, but no longer than
//     joinTimeout, so a side that ignores cancellation cannot hang the caller.
//  4. Errors are classified by ClassifyTunnelErrors: a nil handler result is
//     success regardless of the tunnel's exit; an EOF handler error with a
//     tunnel error is surfaced as a connection error.
//
// The shared context is always cancelled before RunPair returns, so neither
// goroutine outlives the call (barring a side that ignores its context, which
// the joinTimeout bound makes non-fatal).
func (pb *PipeBridge) RunPair(
	ctx context.Context,
	tunnelFn TunnelFunc,
	handlerFn HandlerFunc,
) error {
	pairCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	tunnelSide := func() error { return tunnelFn(pairCtx, pb.StdinReader, pb.StdoutWriter) }
	handlerSide := func() error {
		defer cancel() // the handler is the primary side; if it returns, stop the tunnel
		return handlerFn(pairCtx, pb.StdoutReader, pb.StdinWriter)
	}

	// Run the two sides concurrently and wait for both to finish (or the slower
	// side to be abandoned after joinTimeout).
	tunnelErr, handlerErr := iojoin.Join(
		tunnelSide, handlerSide, joinTimeout,
		func() {
			cancel()
			_ = pb.StdoutWriter.Close()
			_ = pb.StdinWriter.Close()
		},
	)
	return ClassifyTunnelErrors(tunnelErr, handlerErr)
}
