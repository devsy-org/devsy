package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/stretchr/testify/suite"
)

type pipeBridgeClassificationSuite struct{ suite.Suite }

func TestPipeBridgeClassificationSuite(t *testing.T) {
	suite.Run(t, new(pipeBridgeClassificationSuite))
}

func (s *pipeBridgeClassificationSuite) TestTunnelErrorWinsOverInternalCancellation() {
	agentErr := errors.New("agent command failed")
	err := s.runPair(
		context.Background(),
		func(context.Context, *os.File, *os.File) error { return agentErr },
		func(ctx context.Context, _, _ *os.File) error { <-ctx.Done(); return ctx.Err() },
	)
	s.ErrorIs(err, agentErr)
}

func (s *pipeBridgeClassificationSuite) TestTunnelErrorAfterTeardownDoesNotOverrideHandler() {
	err := s.runPair(
		context.Background(),
		func(ctx context.Context, _, _ *os.File) error { <-ctx.Done(); return errors.New("teardown failed") },
		func(context.Context, *os.File, *os.File) error { return context.Canceled },
	)
	s.ErrorIs(err, context.Canceled)
}

func (s *pipeBridgeClassificationSuite) TestEarlierTunnelFailureWinsOverHandlerFailure() {
	agentErr := errors.New("agent command failed")
	handlerErr := errors.New("handler failed")
	err := s.runPair(
		context.Background(),
		func(context.Context, *os.File, *os.File) error { return agentErr },
		func(ctx context.Context, _, _ *os.File) error { <-ctx.Done(); return handlerErr },
	)
	s.ErrorIs(err, agentErr)
	s.NotErrorIs(err, handlerErr)
}

func (s *pipeBridgeClassificationSuite) TestTunnelErrorWinsOverClosedHandlerPipe() {
	agentErr := errors.New("agent command failed")
	err := s.runPair(context.Background(),
		func(context.Context, *os.File, *os.File) error { return agentErr },
		func(ctx context.Context, _, _ *os.File) error {
			<-ctx.Done()
			return fmt.Errorf("write: %w", os.ErrClosed)
		},
	)
	s.ErrorIs(err, agentErr)
}

func (s *pipeBridgeClassificationSuite) TestCompletedHandlerResultSurvivesTunnelExit() {
	var result *config2.Result
	resultSent := make(chan struct{})
	err := s.runPair(
		context.Background(),
		func(context.Context, *os.File, *os.File) error {
			<-resultSent
			return errors.New("SSH command failed with exit code 1")
		},
		func(ctx context.Context, _, _ *os.File) error {
			result = &config2.Result{Error: "workspace changed; use --recreate"}
			close(resultSent)
			<-ctx.Done()
			return nil
		},
	)
	s.NoError(err)
	s.Require().NotNil(result)
	s.EqualError(result.Err(), "workspace changed; use --recreate")
}

func (s *pipeBridgeClassificationSuite) TestHandlerFailureRecordedBeforeTunnelError() {
	tunnelStarted := make(chan struct{})
	handlerErr := errors.New("handler failed")
	err := s.runPair(context.Background(),
		func(ctx context.Context, _, _ *os.File) error {
			close(tunnelStarted)
			<-ctx.Done()
			return errors.New("closed resource")
		},
		func(context.Context, *os.File, *os.File) error {
			<-tunnelStarted
			return handlerErr
		},
	)
	s.ErrorIs(err, handlerErr)
	s.Equal("tunnel to container: handler failed", err.Error())
}

func (s *pipeBridgeClassificationSuite) TestSuccessfulTunnelBeforeHandlerReportsIncompleteOperation() {
	err := s.runPair(context.Background(),
		func(context.Context, *os.File, *os.File) error { return nil },
		func(ctx context.Context, _, _ *os.File) error { <-ctx.Done(); return ctx.Err() },
	)
	s.EqualError(err, "tunnel closed before handler completed")
	s.NotErrorIs(err, context.Canceled)
}

func (s *pipeBridgeClassificationSuite) TestHandlerDeadlineAfterSuccessfulTunnel() {
	err := s.runPair(
		context.Background(),
		func(context.Context, *os.File, *os.File) error { return nil },
		func(ctx context.Context, _, _ *os.File) error { <-ctx.Done(); return context.DeadlineExceeded },
	)
	s.ErrorIs(err, context.DeadlineExceeded)
}

func (s *pipeBridgeClassificationSuite) TestParentCancellationWins() {
	ctx, cancel := context.WithCancel(context.Background())
	agentErr := errors.New("agent command failed")
	err := s.runPair(
		ctx,
		func(context.Context, *os.File, *os.File) error { cancel(); return agentErr },
		func(ctx context.Context, _, _ *os.File) error { <-ctx.Done(); return ctx.Err() },
	)
	s.ErrorIs(err, context.Canceled)
}

func (s *pipeBridgeClassificationSuite) TestHandlerDeadlinePreservesTunnelError() {
	agentErr := errors.New("agent command failed")
	err := s.runPair(
		context.Background(),
		func(context.Context, *os.File, *os.File) error { return agentErr },
		func(ctx context.Context, _, _ *os.File) error {
			<-ctx.Done()
			return fmt.Errorf("handler: %w", context.DeadlineExceeded)
		},
	)
	s.ErrorIs(err, agentErr)
}

func (s *pipeBridgeClassificationSuite) TestParentDeadlineWins() {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	err := s.runPair(
		ctx,
		func(context.Context, *os.File, *os.File) error { return errors.New("agent failed") },
		func(ctx context.Context, _, _ *os.File) error { <-ctx.Done(); return ctx.Err() },
	)
	s.ErrorIs(err, context.DeadlineExceeded)
}

func (s *pipeBridgeClassificationSuite) TestWrappedTunnelCancellationDoesNotReplaceHandlerError() {
	for _, tunnelErr := range []error{context.Canceled, context.DeadlineExceeded} {
		err := s.runPair(
			context.Background(),
			func(context.Context, *os.File, *os.File) error { return fmt.Errorf("tunnel: %w", tunnelErr) },
			func(ctx context.Context, _, _ *os.File) error { <-ctx.Done(); return context.Canceled },
		)
		s.Equal("tunnel to container: context canceled", err.Error())
	}
}

func (s *pipeBridgeClassificationSuite) TestBothNil() {
	err := s.runPair(
		context.Background(),
		func(context.Context, *os.File, *os.File) error { return nil },
		func(context.Context, *os.File, *os.File) error { return nil },
	)
	s.NoError(err)
}

func (s *pipeBridgeClassificationSuite) TestConnectionErrorClassification() {
	agentErr := errors.New("dial failed")
	err := s.runPair(
		context.Background(),
		func(context.Context, *os.File, *os.File) error { return agentErr },
		func(context.Context, *os.File, *os.File) error { return io.EOF },
	)
	s.ErrorIs(err, agentErr)
	s.Equal("connect to server: dial failed", err.Error())
}

func (s *pipeBridgeClassificationSuite) runPair(
	ctx context.Context,
	tunnelFn TunnelFunc,
	handlerFn HandlerFunc,
) error {
	bridge, err := NewPipeBridge()
	s.Require().NoError(err)
	defer bridge.Close()
	return bridge.RunPair(ctx, tunnelFn, handlerFn)
}
