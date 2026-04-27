package tunnel

import (
	"context"
	"os"
)

type PipeBridge struct {
	StdoutReader *os.File
	StdoutWriter *os.File
	StdinReader  *os.File
	StdinWriter  *os.File
}

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

func (pb *PipeBridge) Close() {
	_ = pb.StdoutReader.Close()
	_ = pb.StdoutWriter.Close()
	_ = pb.StdinReader.Close()
	_ = pb.StdinWriter.Close()
}

type (
	TunnelFunc  func(ctx context.Context, stdin *os.File, stdout *os.File) error
	HandlerFunc func(ctx context.Context, stdout *os.File, stdin *os.File) error
)

func (pb *PipeBridge) RunPair(
	ctx context.Context,
	tunnelFn TunnelFunc,
	handlerFn HandlerFunc,
) error {
	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	tunnelChan := make(chan error, 1)
	go func() {
		tunnelChan <- tunnelFn(cancelCtx, pb.StdinReader, pb.StdoutWriter)
	}()

	handlerChan := make(chan error, 1)
	go func() {
		defer cancel()
		handlerChan <- handlerFn(cancelCtx, pb.StdoutReader, pb.StdinWriter)
	}()

	return awaitPair(cancel, tunnelChan, handlerChan, pb.StdoutWriter, pb.StdinWriter)
}

func awaitPair(
	cancel context.CancelFunc,
	tunnelChan, handlerChan <-chan error,
	stdoutWriter, stdinWriter *os.File,
) error {
	var tunnelErr, handlerErr error

	select {
	case handlerErr = <-handlerChan:
		select {
		case tunnelErr = <-tunnelChan:
		default:
		}
	case tunnelErr = <-tunnelChan:
		cancel()
		_ = stdoutWriter.Close()
		_ = stdinWriter.Close()
		handlerErr = <-handlerChan
	}

	return ClassifyTunnelErrors(tunnelErr, handlerErr)
}
