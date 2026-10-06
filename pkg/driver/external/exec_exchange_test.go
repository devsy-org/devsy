package external

import (
	"context"
	"errors"
	"io"
	"testing/iotest"

	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"github.com/devsy-org/devsy/pkg/driver"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *HostSuite) TestExecInputFailureSurvivesBufferedSuccessfulExit() {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	want := errors.New("stdin read failed before command completion")
	stream := &completedExecStream{ready: ctx.Done()}
	exit, err := exchangeExec(ctx, cancel, stream, driver.Streams{
		Stdin: iotest.ErrReader(want), Stdout: io.Discard, Stderr: io.Discard,
	})
	s.ErrorIs(err, want)
	s.Nil(exit)
}

func (s *HostSuite) TestExecInputFailureAtCleanupBoundarySurvivesSuccessfulExit() {
	for _, completionFirst := range []bool{false, true} {
		name := "input failure wins cancellation"
		if completionFirst {
			name = "completion wins cancellation"
		}
		s.Run(name, func() {
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			want := errors.New("stdin failure at cleanup boundary")
			input, producer := io.Pipe()
			defer func() { _ = producer.Close() }()
			ready := make(chan struct{})
			inputFailed := make(chan struct{})
			cancelAtBoundary := func(cause error) {
				if errors.Is(cause, want) {
					cancel(cause)
					close(inputFailed)
					return
				}
				if completionFirst {
					cancel(cause)
				}
				_ = producer.CloseWithError(want)
				<-inputFailed
				cancel(cause)
			}
			exit, err := exchangeExec(
				ctx,
				cancelAtBoundary,
				&completedExecStream{ready: ready},
				driver.Streams{
					Stdin:  &readyInputReader{Reader: input, ready: ready},
					Stdout: io.Discard,
					Stderr: io.Discard,
				},
			)
			s.ErrorIs(err, want)
			s.Nil(exit)
		})
	}
}

func (s *HostSuite) TestExecCleanupInputErrorsPreserveSuccessfulExit() {
	for _, cleanupErr := range []error{
		io.ErrClosedPipe, context.Canceled, status.Error(codes.Canceled, "stream canceled"),
	} {
		s.Run(cleanupErr.Error(), func() {
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			input, producer := io.Pipe()
			defer func() { _ = producer.Close() }()
			ready := make(chan struct{})
			cancelAtBoundary := func(cause error) {
				cancel(cause)
				_ = producer.CloseWithError(cleanupErr)
			}
			exit, err := exchangeExec(
				ctx,
				cancelAtBoundary,
				&completedExecStream{ready: ready},
				driver.Streams{
					Stdin:  &readyInputReader{Reader: input, ready: ready},
					Stdout: io.Discard,
					Stderr: io.Discard,
				},
			)
			s.NoError(err)
			s.NotNil(exit)
		})
	}
}

// Models a terminal exit already buffered when an input failure cancels the RPC.
type completedExecStream struct {
	runtimev1.RuntimeDriver_ExecClient
	ready    <-chan struct{}
	exitSent bool
}

func (s *completedExecStream) Recv() (*runtimev1.ExecServerMessage, error) {
	if s.exitSent {
		return nil, io.EOF
	}
	<-s.ready
	s.exitSent = true
	return &runtimev1.ExecServerMessage{
		Payload: &runtimev1.ExecServerMessage_Exit{Exit: &runtimev1.ExecExit{}},
	}, nil
}

// Starts completion only after the input pump is blocked inside Read.
type readyInputReader struct {
	io.Reader
	ready chan struct{}
}

func (r *readyInputReader) Read(p []byte) (int, error) {
	close(r.ready)
	return r.Reader.Read(p)
}
