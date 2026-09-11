package status

import "github.com/devsy-org/devsy/pkg/log"

// logReporter renders events as debug log lines.
type logReporter struct{}

// NewLogReporter returns a Reporter that logs each event at debug level.
func NewLogReporter() Reporter { return logReporter{} }

func (logReporter) Report(e Event) {
	pipeline := string(e.Pipeline)
	if pipeline == "" {
		pipeline = "up"
	}
	state := e.State
	switch state {
	case StateFailed:
		message := ""
		if e.Error != nil {
			message = e.Error.Message
		}
		log.Debugf("%s: phase %q failed: %s", pipeline, e.Phase, message)
	case StateStarted:
		log.Debugf("%s: entering phase %q: %s", pipeline, e.Phase, e.Step)
	case StateSkipped:
		log.Debugf("%s: skipped phase %q: %s", pipeline, e.Phase, e.Step)
	default:
		log.Debugf("%s: completed phase %q: %s", pipeline, e.Phase, e.Step)
	}
}
