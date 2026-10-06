package external

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/subprocess"
)

var (
	_ driver.Driver         = (*Host)(nil)
	_ driver.ArgvExecDriver = (*Host)(nil)
)

// CommandExitError describes an ordinary command failure, distinct from an RPC failure.
type CommandExitError struct {
	Code   int32
	Signal string
}

func (e *CommandExitError) Error() string {
	if e.Signal != "" {
		return fmt.Sprintf("external runtime command terminated by signal %q", e.Signal)
	}
	return fmt.Sprintf("external runtime command exited with code %d", e.Code)
}

func (e *CommandExitError) ExitCode() int { return int(e.Code) }

// CommandDevContainer uses the container shell; RawStdout preserves protocol bytes.
// Once streaming starts, the host closes closable stdin on completion or cancellation.
// Blocking readers must unblock on Close or caller cancellation; writers must return
// from Write. Arbitrary io.Reader/io.Writer calls cannot be interrupted by a context.
func (h *Host) CommandDevContainer(ctx context.Context, params *driver.CommandParams) error {
	if params == nil {
		return errors.New("external runtime command parameters are missing")
	}
	return h.exec(ctx, &runtimev1.ExecStart{
		WorkspaceId: params.WorkspaceID,
		Argv:        []string{"/bin/sh", "-c", params.Command},
		User:        params.User,
	}, driver.Streams{Stdin: params.Stdin, Stdout: params.Stdout, Stderr: params.Stderr}, params.RawStdout)
}

// CommandContainerArgv preserves literal argv and raw stdout for binary agent injection.
// Stdin ownership and cancellation follow CommandDevContainer.
func (h *Host) CommandContainerArgv(
	ctx context.Context,
	workspaceID string,
	argv []string,
	streams driver.Streams,
) error {
	return h.exec(
		ctx,
		&runtimev1.ExecStart{WorkspaceId: workspaceID, Argv: argv, User: "root"},
		streams,
		true,
	)
}

func (h *Host) exec(
	ctx context.Context,
	start *runtimev1.ExecStart,
	streams driver.Streams,
	raw bool,
) error {
	if start.WorkspaceId == "" || len(start.Argv) == 0 || start.Argv[0] == "" {
		return errors.New("external runtime Exec requires workspace ID and a nonempty executable")
	}
	operation := h.forWorkspace(start.WorkspaceId, nil)
	output, flush := operation.commandOutput(streams, raw)
	var exit *runtimev1.ExecExit
	err := operation.call(ctx, "Exec", func(client runtimev1.RuntimeDriverClient) error {
		var err error
		exit, err = execStream(ctx, client, start, output)
		return err
	})
	return operation.commandResult(ctx, exit, err, flush())
}

func (h *Host) commandResult(
	ctx context.Context,
	exit *runtimev1.ExecExit,
	err, flushErr error,
) error {
	if err != nil {
		return err
	}
	if flushErr != nil {
		return h.operationError(ctx, "Exec output", flushErr)
	}
	if exit.ExitCode != 0 || exit.Signal != "" {
		return &CommandExitError{
			Code:   exit.ExitCode,
			Signal: h.redactor.Redact(exit.Signal),
		}
	}
	return nil
}

func (h *Host) commandOutput(streams driver.Streams, raw bool) (driver.Streams, func() error) {
	if streams.Stdout == nil {
		streams.Stdout = io.Discard
	}
	if streams.Stderr == nil {
		streams.Stderr = io.Discard
	}
	stdout := &subprocess.StreamingRedactingWriter{Next: streams.Stdout, Redactor: h.redactor}
	stderr := &subprocess.StreamingRedactingWriter{Next: streams.Stderr, Redactor: h.redactor}
	if !raw {
		streams.Stdout = stdout
	}
	streams.Stderr = stderr
	return streams, func() error { return errors.Join(stdout.Flush(), stderr.Flush()) }
}

func execStream(
	ctx context.Context,
	client runtimev1.RuntimeDriverClient,
	start *runtimev1.ExecStart,
	streams driver.Streams,
) (*runtimev1.ExecExit, error) {
	streamCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stream, err := client.Exec(streamCtx)
	if err != nil {
		return nil, err
	}
	if err := stream.Send(&runtimev1.ExecClientMessage{
		Payload: &runtimev1.ExecClientMessage_Start{Start: start},
	}); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	exit, err := exchangeExec(streamCtx, cancel, stream, streams)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return exit, err
}

func exchangeExec(
	ctx context.Context,
	cancel context.CancelCauseFunc,
	stream runtimev1.RuntimeDriver_ExecClient,
	streams driver.Streams,
) (*runtimev1.ExecExit, error) {
	closeInput := sync.OnceFunc(func() {
		if closer, ok := streams.Stdin.(io.Closer); ok {
			_ = closer.Close()
		}
	})
	stop := context.AfterFunc(ctx, closeInput)
	defer stop()
	inputDone := make(chan struct{})
	go func() {
		defer close(inputDone)
		err := sendExecInput(ctx, stream, streams.Stdin)
		if err != nil && !errors.Is(err, io.EOF) {
			cancel(err)
		}
	}()
	exit, receiveErr := receiveExecOutput(stream, streams)
	inputCause := context.Cause(ctx)
	// Cancel before joining: Send may be flow-controlled and Read may be blocked.
	cancel(nil)
	closeInput()
	<-inputDone
	if inputCause != nil {
		return nil, inputCause
	}
	// Recv owns command completion, including a command that exits before stdin EOF.
	return exit, receiveErr
}
