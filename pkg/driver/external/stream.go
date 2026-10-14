package external

import (
	"bytes"
	"context"
	"errors"
	"io"

	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"github.com/devsy-org/devsy/pkg/driver"
)

func sendExecInput(
	ctx context.Context,
	stream runtimev1.RuntimeDriver_ExecClient,
	input io.Reader,
) error {
	if input != nil {
		if err := sendExecData(ctx, stream, input); err != nil {
			return err
		}
	}
	if err := stream.Send(&runtimev1.ExecClientMessage{
		Payload: &runtimev1.ExecClientMessage_CloseStdin{CloseStdin: &runtimev1.CloseStdin{}},
	}); err != nil {
		return err
	}
	return stream.CloseSend()
}

func sendExecData(
	ctx context.Context,
	stream runtimev1.RuntimeDriver_ExecClient,
	input io.Reader,
) error {
	buffer := make([]byte, runtimev1.ChunkSize)
	for ctx.Err() == nil {
		n, err := readExecChunk(ctx, input, buffer)
		if n > 0 {
			if sendErr := stream.Send(&runtimev1.ExecClientMessage{
				Payload: &runtimev1.ExecClientMessage_Stdin{Stdin: bytes.Clone(buffer[:n])},
			}); sendErr != nil {
				return sendErr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
	return ctx.Err()
}

func readExecChunk(ctx context.Context, input io.Reader, buffer []byte) (int, error) {
	// Match bufio's bounded tolerance of readers returning (0, nil).
	for range 100 {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		n, err := input.Read(buffer)
		if n != 0 || err != nil {
			return n, err
		}
	}
	return 0, io.ErrNoProgress
}

func receiveExecOutput(
	stream runtimev1.RuntimeDriver_ExecClient,
	streams driver.Streams,
) (*runtimev1.ExecExit, error) {
	var exit *runtimev1.ExecExit
	for {
		frame, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			if exit == nil {
				return nil, errors.New("runtime Exec ended without a terminal exit")
			}
			return exit, nil
		}
		if err != nil {
			return nil, err
		}
		if exit != nil {
			return nil, errors.New("runtime Exec sent a frame after its terminal exit")
		}
		if terminal := frame.GetExit(); terminal != nil {
			exit = terminal
			continue
		}
		if err := writeExecFrame(frame, streams); err != nil {
			return nil, err
		}
	}
}

func writeExecFrame(frame *runtimev1.ExecServerMessage, streams driver.Streams) error {
	switch payload := frame.GetPayload().(type) {
	case *runtimev1.ExecServerMessage_Stdout:
		return writeOutputChunk(streams.Stdout, payload.Stdout)
	case *runtimev1.ExecServerMessage_Stderr:
		return writeOutputChunk(streams.Stderr, payload.Stderr)
	default:
		return errors.New("runtime Exec sent an invalid output frame")
	}
}

func writeOutputChunk(writer io.Writer, chunk *runtimev1.OutputChunk) error {
	data := chunk.GetData()
	if len(data) == 0 || len(data) > runtimev1.ChunkSize {
		return errors.New("runtime output chunk must contain 1..32768 bytes")
	}
	n, err := writer.Write(data)
	if err == nil && n != len(data) {
		return io.ErrShortWrite
	}
	return err
}
