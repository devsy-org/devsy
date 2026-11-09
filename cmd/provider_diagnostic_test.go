package cmd

import (
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/devsy-org/devsy/pkg/clierr"
	"github.com/stretchr/testify/suite"
	"golang.org/x/crypto/ssh"
)

type (
	providerDiagnosticSuite struct{ suite.Suite }
	diagnosticExit          struct{ err error }
)

func (e diagnosticExit) Error() string           { return fmt.Sprintf("provider diagnostic: %v", e.err) }
func (e diagnosticExit) Unwrap() error           { return e.err }
func (e diagnosticExit) AgentDiagnostic() string { return e.Error() }

func TestProviderDiagnosticSuite(t *testing.T) { suite.Run(t, new(providerDiagnosticSuite)) }

func (s *providerDiagnosticSuite) TestExitPreservesStatusAndDiagnostic() {
	for _, machine := range []bool{false, true} {
		s.Run(fmt.Sprintf("machine=%t", machine), func() {
			reader, writer, err := os.Pipe()
			s.Require().NoError(err)
			old := os.Stderr
			os.Stderr = writer
			exit := &ssh.ExitError{}
			code, ok := passthroughExitCode(diagnosticExit{err: exit}, machine)
			os.Stderr = old
			s.Require().NoError(writer.Close())
			output, err := io.ReadAll(reader)
			s.Require().NoError(err)
			s.Require().NoError(reader.Close())
			s.True(ok)
			s.Equal(exit.ExitStatus(), code)
			s.Contains(string(output), "provider diagnostic")
		})
	}
}

func (s *providerDiagnosticSuite) TestExitCodeRetainsDiagnosticForClassifiedText() {
	reader, writer, err := os.Pipe()
	s.Require().NoError(err)
	old := os.Stderr
	os.Stderr = writer
	exit := &ssh.ExitError{}
	wrapped := diagnosticExit{err: fmt.Errorf("Cannot connect to the Docker daemon: %w", exit)}
	code := exitCodeForError(wrapped, true)
	os.Stderr = old
	s.Require().NoError(writer.Close())
	output, err := io.ReadAll(reader)
	s.Require().NoError(err)
	s.Require().NoError(reader.Close())
	s.Equal(exit.ExitStatus(), code)
	s.Contains(string(output), "Cannot connect to the Docker daemon")
}

func (s *providerDiagnosticSuite) TestClassifiedWrapperRetainsAgentDiagnostic() {
	reader, writer, err := os.Pipe()
	s.Require().NoError(err)
	old := os.Stderr
	os.Stderr = writer
	exit := &ssh.ExitError{}
	wrapped := clierr.WithContext(
		diagnosticExit{err: fmt.Errorf("Cannot connect to the Docker daemon: %w", exit)},
		map[string]string{"run": "test"},
	)
	code := exitCodeForError(wrapped, true)
	os.Stderr = old
	s.Require().NoError(writer.Close())
	output, err := io.ReadAll(reader)
	s.Require().NoError(err)
	s.Require().NoError(reader.Close())
	s.Equal(exit.ExitStatus(), code)
	s.Contains(string(output), "Cannot connect to the Docker daemon")
}
