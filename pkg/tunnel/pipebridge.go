package tunnel

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/log"
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
func (pb *PipeBridge) RunPair(
	ctx context.Context,
	tunnelFn TunnelFunc,
	handlerFn HandlerFunc,
) error {
	pairCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			cancel()
			_ = pb.StdoutWriter.Close()
			_ = pb.StdinWriter.Close()
		})
	}
	defer stop()

	// If the parent ctx is cancelled before either side returns (e.g. both
	// are blocked in a raw file Read that doesn't itself watch ctx), stop
	// them directly instead of waiting on iojoin.Join's onFirst, which never
	// fires unless one side already completed.
	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		select {
		case <-ctx.Done():
			stop()
		case <-stopped:
		}
	}()

	tunnelSide := func() error { return tunnelFn(pairCtx, pb.StdinReader, pb.StdoutWriter) }
	handlerSide := func() error {
		return handlerFn(pairCtx, pb.StdoutReader, pb.StdinWriter)
	}

	// Run the two sides concurrently and wait for both to finish (or the slower
	// side to be abandoned after joinTimeout).
	tunnelErr, handlerErr := iojoin.Join(tunnelSide, handlerSide, joinTimeout, stop)
	log.Debugf(
		"pipe bridge completed: tunnel_err=%v handler_err=%v parent_err=%v pair_err=%v",
		tunnelErr,
		handlerErr,
		ctx.Err(),
		pairCtx.Err(),
	)
	return ClassifyTunnelErrors(tunnelErr, handlerErr)
}
