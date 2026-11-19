package status

import (
	"context"
	"log/slog"

	"github.com/devsy-org/devsy/pkg/log"
)

// logReporter renders events as debug log lines.
type logReporter struct{}

// NewLogReporter returns a Reporter that logs each event at debug level.
func NewLogReporter() Reporter { return logReporter{} }

func (logReporter) Report(e Event) {
	code := ""
	if e.Error != nil {
		code = e.Error.Code
	}
	log.DebugContext(context.Background(), "status",
		slog.String("pipeline", string(e.Pipeline)),
		slog.String("phase", string(e.Phase)),
		slog.String("step", e.Step),
		slog.String("operation_id", e.OperationID),
		slog.String("parent_operation_id", e.ParentOperationID),
		slog.String("state", string(e.State)),
		slog.Int64("duration_ms", e.Duration.Milliseconds()),
		slog.String("error_code", code),
	)
}
