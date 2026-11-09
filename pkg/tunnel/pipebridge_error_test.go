package tunnel

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/suite"
)

type pipeBridgeErrorSuite struct{ suite.Suite }

func TestPipeBridgeErrorSuite(t *testing.T) { suite.Run(t, new(pipeBridgeErrorSuite)) }

func (s *pipeBridgeErrorSuite) TestCommandFailureIsNotMaskedByInternalCancellation() {
	bridge, err := NewPipeBridge()
	s.Require().NoError(err)
	defer bridge.Close()
	commandErr := errors.New("provider binary checksum mismatch")
	err = bridge.RunPair(
		context.Background(),
		func(context.Context, *os.File, *os.File) error { return commandErr },
		func(ctx context.Context, _, _ *os.File) error { <-ctx.Done(); return ctx.Err() },
	)
	s.ErrorIs(err, commandErr)
}

func (s *pipeBridgeErrorSuite) TestClosedPipeDoesNotMaskCommandFailure() {
	commandErr := errors.New("command failed")
	err := classifyBridgeErrors(
		context.Background(),
		commandErr,
		&os.PathError{Op: "write", Path: "pipe", Err: os.ErrClosed},
	)
	s.ErrorIs(err, commandErr)
}

func (s *pipeBridgeErrorSuite) TestCancellationClassification() {
	for _, tc := range []struct {
		name         string
		cancelParent bool
	}{
		{name: "internal cancellation"},
		{name: "parent cancellation", cancelParent: true},
	} {
		s.Run(tc.name, func() {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancelParent {
				cancel()
			}
			commandErr := errors.New("command failed")
			err := classifyBridgeErrors(
				ctx,
				commandErr,
				fmt.Errorf("handler: %w", context.Canceled),
			)
			if tc.cancelParent {
				s.ErrorIs(err, context.Canceled)
			} else {
				s.ErrorIs(err, commandErr)
			}
		})
	}
}
