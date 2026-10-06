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
	stream := &completedExecStream{ctx: ctx}
	exit, err := exchangeExec(ctx, cancel, stream, driver.Streams{
		Stdin: iotest.ErrReader(want), Stdout: io.Discard, Stderr: io.Discard,
	})
	s.ErrorIs(err, want)
	s.Nil(exit)
}

// Models a terminal exit already buffered when an input failure cancels the RPC.
type completedExecStream struct {
	runtimev1.RuntimeDriver_ExecClient
	ctx      context.Context
	exitSent bool
}

func (s *completedExecStream) Recv() (*runtimev1.ExecServerMessage, error) {
	if s.exitSent {
		return nil, io.EOF
	}
	<-s.ctx.Done()
	s.exitSent = true
	return &runtimev1.ExecServerMessage{
		Payload: &runtimev1.ExecServerMessage_Exit{Exit: &runtimev1.ExecExit{}},
	}, nil
}
