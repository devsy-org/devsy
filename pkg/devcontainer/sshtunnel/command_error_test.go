package sshtunnel

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/suite"
)

type commandErrorSuite struct{ suite.Suite }

func TestCommandErrorSuite(t *testing.T) { suite.Run(t, new(commandErrorSuite)) }

func (s *commandErrorSuite) TestCommandFailure() {
	for _, tc := range []struct {
		name       string
		output     string
		wrapsError bool
	}{
		{name: "agent diagnostic", output: "provider binary checksum mismatch"},
		{name: "no diagnostic", wrapsError: true},
	} {
		s.Run(tc.name, func() {
			exit := errors.New("exit status 1")
			err := commandFailure(exit, tc.output)
			s.ErrorContains(err, "exit status 1")
			if tc.wrapsError {
				s.ErrorIs(err, exit)
			} else {
				s.ErrorContains(err, tc.output)
				s.NotErrorIs(err, exit)
			}
		})
	}
}

func (s *commandErrorSuite) TestCommandFailurePreservesCancellation() {
	err := commandFailure(context.Canceled, "agent diagnostic")
	s.ErrorIs(err, context.Canceled)
	s.ErrorContains(err, "agent diagnostic")
}
