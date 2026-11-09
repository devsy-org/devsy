package tunnel

import (
	"context"
	"errors"
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
