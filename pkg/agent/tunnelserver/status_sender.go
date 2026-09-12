package tunnelserver

import (
	"context"
	"os"
	"time"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/devsy-org/devsy/pkg/status"
)

// NewTunnelStatusReporter returns a status.Reporter that forwards each event
// to client over the Status RPC, mirroring NewTunnelLogger's buffered-worker
// pattern so a slow/unavailable peer cannot stall the up pipeline.
func NewTunnelStatusReporter(ctx context.Context, client tunnel.TunnelClient) status.Reporter {
	r := &tunnelStatusReporter{
		ctx:    ctx,
		client: client,
		events: make(chan *tunnel.StatusUpdate, 1000),
	}
	go r.worker()
	return r
}

type tunnelStatusReporter struct {
	ctx    context.Context
	client tunnel.TunnelClient
	events chan *tunnel.StatusUpdate
}

func (r *tunnelStatusReporter) Report(e status.Event) {
	redactor := secrets.NewEnvironmentRedactor(os.Environ())
	select {
	case r.events <- &tunnel.StatusUpdate{
		Phase:             redactor.Redact(string(e.Phase)),
		Step:              redactor.Redact(e.Step),
		State:             string(e.State),
		DurationMs:        e.Duration.Milliseconds(),
		OperationId:       redactor.Redact(e.OperationID),
		ParentOperationId: redactor.Redact(e.ParentOperationID),
		Error: func() *tunnel.StatusError {
			if e.Error != nil {
				return &tunnel.StatusError{
					Code:    redactor.Redact(e.Error.Code),
					Message: redactor.Redact(e.Error.Message),
					Hint:    redactor.Redact(e.Error.Hint),
					Context: redactContext(e.Error.Context, redactor),
				}
			}
			return nil
		}(),
	}:
	case <-r.ctx.Done():
	}
}

func redactContext(values map[string]string, redactor *secrets.Redactor) map[string]string {
	if len(values) == 0 {
		return nil
	}
	redacted := make(map[string]string, len(values))
	for key, value := range values {
		redacted[redactor.Redact(key)] = redactor.Redact(value)
	}
	return redacted
}

func (r *tunnelStatusReporter) worker() {
	for {
		select {
		case update := <-r.events:
			ctx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
			_, _ = r.client.Status(ctx, update)
			cancel()
		case <-r.ctx.Done():
			return
		}
	}
}
