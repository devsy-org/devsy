package sshtunnel

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/devsy-org/devsy/pkg/log"
	devssh "github.com/devsy-org/devsy/pkg/ssh"
	"github.com/stretchr/testify/suite"
)

const failureDiagnostics = `{"level":"debug","ts":"2026-10-13T22:41:48.610Z","msg":"credentials server started"}
{"kind":"error","outcome":"error","code":"UNKNOWN","message":"command failed with exit code 1"}`

var errSentinelCause = errors.New("sentinel cause")

type AgentCommandFailureTestSuite struct {
	suite.Suite
}

func TestAgentCommandFailureTestSuite(t *testing.T) {
	suite.Run(t, new(AgentCommandFailureTestSuite))
}

func (s *AgentCommandFailureTestSuite) TestErrorKeepsDiagnosticsAndLogSummaryOmitsThem() {
	cause := errors.New("exit status 1")

	withDiagnostics := &agentCommandFailure{cause: cause, diagnostics: failureDiagnostics}
	s.Equal(
		fmt.Errorf("run agent command failed: %w\n%s", cause, failureDiagnostics).Error(),
		withDiagnostics.Error(),
	)
	s.Same(cause, withDiagnostics.Unwrap())
	s.Equal("run agent command failed: exit status 1", withDiagnostics.LogSummary())

	empty := &agentCommandFailure{cause: cause}
	s.Equal("run agent command failed: exit status 1", empty.Error())
	s.Equal(empty.Error(), empty.LogSummary())
}

func (s *AgentCommandFailureTestSuite) TestWrappedFailureMatchesCauseAndType() {
	exitErr := &devssh.ExitError{ExitCode: 1}
	for _, tc := range []struct {
		name   string
		cause  error
		target error
	}{
		{"context canceled", context.Canceled, context.Canceled},
		{"sentinel", errSentinelCause, errSentinelCause},
		{"ssh exit error", exitErr, exitErr},
	} {
		s.Run(tc.name, func() {
			failure := &agentCommandFailure{cause: tc.cause, diagnostics: failureDiagnostics}
			wrapped := fmt.Errorf("outer: %w", fmt.Errorf("middle: %w", failure))

			s.ErrorIs(wrapped, tc.target)
			s.ErrorIs(failure, tc.target)

			var asFailure *agentCommandFailure
			s.Require().ErrorAs(wrapped, &asFailure)
			s.Same(failure, asFailure)

			s.Equal(failure.LogSummary(), log.ErrorForLog(wrapped))
		})
	}

	var exit *devssh.ExitError
	failure := &agentCommandFailure{cause: fmt.Errorf("wrap: %w", exitErr)}
	s.Require().ErrorAs(fmt.Errorf("outer: %w", failure), &exit)
	s.Equal(1, exit.ExitCode)
}

func (s *AgentCommandFailureTestSuite) TestErrorForLogOmitsDiagnostics() {
	failure := &agentCommandFailure{cause: errSentinelCause, diagnostics: failureDiagnostics}
	s.Equal("run agent command failed: sentinel cause", log.ErrorForLog(failure))
}
