package cmd

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/stretchr/testify/suite"
)

type rootLiveOutputSuite struct{ suite.Suite }

type canceledUnsafeOutput struct{}

func (canceledUnsafeOutput) Error() string         { return context.Canceled.Error() }
func (canceledUnsafeOutput) Unwrap() error         { return context.Canceled }
func (canceledUnsafeOutput) SkipFinalStatus() bool { return true }

func TestRootLiveOutputSuite(t *testing.T) { suite.Run(t, new(rootLiveOutputSuite)) }

func (s *rootLiveOutputSuite) TestOutputErrorDoesNotWriteToStderr() {
	reader, writer, err := os.Pipe()
	s.Require().NoError(err)
	defer func() { _ = reader.Close() }()
	defer func() { _ = writer.Close() }()
	original := os.Stderr
	os.Stderr = writer
	defer func() { os.Stderr = original }()
	outputErr := &docker.LiveOutputError{Err: errors.New("live output: 12 bytes dropped")}
	code := exitCodeForError(&workspace.CommandExitError{Code: 7, Cause: outputErr}, false)
	s.Equal(7, code)
	s.Equal(1, exitCodeForError(errors.Join(context.Canceled, outputErr), false))
	s.Equal(1, exitCodeForError(canceledUnsafeOutput{}, false))
	s.Require().NoError(writer.Close())
	data, err := io.ReadAll(reader)
	s.Require().NoError(err)
	s.Empty(data)
}
