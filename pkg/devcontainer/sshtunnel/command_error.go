package sshtunnel

import "github.com/devsy-org/devsy/pkg/log"

var _ log.LogSummaryProvider = (*agentCommandFailure)(nil)

const agentCommandFailedPrefix = "run agent command failed: "

// agentCommandFailure pairs a command failure with the redacted stderr tail
// the streamer already logged. Error includes the tail for CLI consumers;
// LogSummary omits it so log sites do not replay it.
type agentCommandFailure struct {
	cause       error
	diagnostics string
}

func (e *agentCommandFailure) Error() string {
	if e.diagnostics == "" {
		return e.LogSummary()
	}
	return e.LogSummary() + "\n" + e.diagnostics
}

func (e *agentCommandFailure) Unwrap() error { return e.cause }

func (e *agentCommandFailure) LogSummary() string {
	return agentCommandFailedPrefix + e.cause.Error()
}
