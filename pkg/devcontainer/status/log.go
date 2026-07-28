package status

import "github.com/devsy-org/devsy/pkg/log"

// logReporter renders events as debug log lines. It is an interim consumer
// until the CLI/desktop stream structured events directly (see
// docs/rfcs/async-workspace-up.md) — real callers should prefer that once it
// lands, but this keeps events observable everywhere in the meantime.
type logReporter struct{}

// NewLogReporter returns a Reporter that logs each event at debug level.
func NewLogReporter() Reporter { return logReporter{} }

func (logReporter) Report(e Event) {
	switch {
	case e.Phase == PhaseFailed:
		log.Debugf("up: phase %q failed: %s", e.Step, e.Err)
	case e.Started:
		log.Debugf("up: entering phase %q", e.Phase)
	default:
		log.Debugf("up: completed phase %q", e.Phase)
	}
}
