package tunnelserver

import (
	"context"
	"time"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/devcontainer/status"
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

func (r *tunnelStatusReporter) Report(e status.Event) {
	select {
	case r.events <- &tunnel.StatusUpdate{
		Phase:   string(e.Phase),
		Step:    e.Step,
		Started: e.Started,
		Error:   e.Err,
	}:
	case <-r.ctx.Done():
	}
}
