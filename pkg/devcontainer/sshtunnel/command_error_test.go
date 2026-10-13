package sshtunnel

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/log"
	devssh "github.com/devsy-org/devsy/pkg/ssh"
	"github.com/stretchr/testify/suite"
)

const failureDiagnostics = `{"level":"debug","ts":"2026-10-13T22:41:48.610Z","msg":"credentials server started"}
{"kind":"error","outcome":"error","code":"UNKNOWN","message":"command failed with exit code 1"}`

var errSentinelCause = errors.New("sentinel cause")

type diagnosticsProvider interface {
	Diagnostics() string
}

type AgentCommandFailureTestSuite struct {
	suite.Suite
}

func TestAgentCommandFailureTestSuite(t *testing.T) {
	suite.Run(t, new(AgentCommandFailureTestSuite))
}

func (s *AgentCommandFailureTestSuite) TestAgentCommandFailureCompatibility() {
	cause := errors.New("exit status 1")

	withDiagnostics := &agentCommandFailure{cause: cause, diagnostics: failureDiagnostics}
	s.Equal(
		fmt.Errorf("run agent command failed: %w\n%s", cause, failureDiagnostics).Error(),
		withDiagnostics.Error(),
	)
	s.Same(cause, withDiagnostics.Unwrap())
	s.Equal("run agent command failed: exit status 1", withDiagnostics.LogSummary())
	s.NotContains(withDiagnostics.LogSummary(), "\n")
	s.NotContains(withDiagnostics.LogSummary(), `{"level"`)
	s.Equal(failureDiagnostics, withDiagnostics.Diagnostics())

	empty := &agentCommandFailure{cause: cause}
	s.Equal(fmt.Errorf("run agent command failed: %w", cause).Error(), empty.Error())
	s.Equal("run agent command failed: exit status 1", empty.Error())
	s.Equal(empty.Error(), empty.LogSummary())
	s.Empty(empty.Diagnostics())
}

func (s *AgentCommandFailureTestSuite) TestWrappedMatching() {
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

			var provider log.LogSummaryProvider
			s.Require().ErrorAs(wrapped, &provider)
			s.Equal(failure.LogSummary(), provider.LogSummary())

			var diag diagnosticsProvider
			s.Require().ErrorAs(wrapped, &diag)
			s.Equal(failureDiagnostics, diag.Diagnostics())

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
	got := log.ErrorForLog(failure)
	s.Equal("run agent command failed: sentinel cause", got)
	s.False(strings.Contains(got, "credentials server started"))
}
