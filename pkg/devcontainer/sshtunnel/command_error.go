package sshtunnel

import "github.com/devsy-org/devsy/pkg/log"

var _ log.LogSummaryProvider = (*agentCommandFailure)(nil)

const agentCommandFailedPrefix = "run agent command failed: "

// agentCommandFailure separates the cause of an agent command failure from the
// redacted diagnostic tail captured from its stderr. Error preserves the legacy
// "cause\ndiagnostics" text for CLI and status consumers; LogSummary omits the
// tail so log sites do not replay records the streamer already emitted.
type agentCommandFailure struct {
	// cause is always non-nil: the error returned by devssh.RunSession.
	cause error
	// diagnostics is redacted and bounded by the JSONLogStreamer.
	diagnostics string
}

func (e *agentCommandFailure) Error() string {
	base := agentCommandFailedPrefix + e.cause.Error()
	if e.diagnostics == "" {
		return base
	}
	return base + "\n" + e.diagnostics
}

func (e *agentCommandFailure) Unwrap() error { return e.cause }

// LogSummary implements log.LogSummaryProvider without the diagnostic tail.
func (e *agentCommandFailure) LogSummary() string {
	return agentCommandFailedPrefix + e.cause.Error()
}

// Diagnostics returns the redacted, bounded stderr tail. It is exposed by
// behavior so callers can errors.As into an interface; never write it to a log.
func (e *agentCommandFailure) Diagnostics() string { return e.diagnostics }
