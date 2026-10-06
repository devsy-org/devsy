package external

import (
	"context"
	"errors"
	"io"
	"testing/iotest"

	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"github.com/devsy-org/devsy/pkg/driver"
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
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	want := errors.New("stdin failure at cleanup boundary")
	input, producer := io.Pipe()
	defer func() { _ = producer.Close() }()
	ready := make(chan struct{})
	close(ready)
	inputFailed := make(chan struct{})
	cancelAtBoundary := func(cause error) {
		if errors.Is(cause, want) {
			cancel(cause)
			close(inputFailed)
			return
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
			Stdin: input, Stdout: io.Discard, Stderr: io.Discard,
		},
	)
	s.ErrorIs(err, want)
	s.Nil(exit)
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
