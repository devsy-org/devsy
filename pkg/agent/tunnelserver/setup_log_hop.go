package tunnelserver

import (
	"context"
	"path/filepath"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/workspacelogs"
)

// SetupLogHop wires one process's side of setup history mirroring: it sends
// this process's own records to the next host, accepts records mirrored from the
// previous host, and forwards those on. Every method is safe on a nil hop, so a
// process without history changes nothing.
type SetupLogHop struct {
	sender    *tunnel.SetupLogSender
	forwarder *tunnel.SetupLogForwarder
	sink      *workspacelogs.Sink
}

// HopOptions selects what a hop does.
type HopOptions struct {
	// Session is this process's run session.
	Session *workspacelogs.Session
	// Upstream is the client of the next host. Nil makes the hop receive-only.
	Upstream tunnel.TunnelClient
	// Receive accepts records mirrored from the previous host.
	Receive bool
}

// NewSetupLogHop builds the hop. It returns nil when the session is nil or the
// hop would do nothing.
func NewSetupLogHop(ctx context.Context, opts HopOptions) *SetupLogHop {
	if opts.Session == nil || (opts.Upstream == nil && !opts.Receive) {
		return nil
	}
	acks, err := workspacelogs.NewAckStore(filepath.Join(opts.Session.Store.Root(), "mirror"))
	if err != nil {
		return nil
	}
	hop := &SetupLogHop{}
	if opts.Upstream != nil {
		source := workspacelogs.NewStoreSource(opts.Session.Store)
		hop.sender = tunnel.NewSetupLogSender(ctx, tunnel.SetupLogSenderOptions{
			Client: opts.Upstream, Source: source, RunID: opts.Session.RunID,
			ProducerID: opts.Session.ProducerID(), Acks: acks,
		})
		hop.forwarder = tunnel.NewSetupLogForwarder(ctx, tunnel.SetupLogForwarderOptions{
			Client: opts.Upstream, Source: source, RunID: opts.Session.RunID, Acks: acks,
		})
	}
	if opts.Receive {
		hop.sink = newHopSink(opts.Session, acks, hop.forwarder)
	}

	return hop
}

func newHopSink(
	session *workspacelogs.Session,
	acks *workspacelogs.AckStore,
	fwd *tunnel.SetupLogForwarder,
) *workspacelogs.Sink {
	m, err := session.Store.Manifest(session.RunID)
	if err != nil {
		return nil
	}
	spec := workspacelogs.RunSpec{RunID: m.RunID, Context: m.Context, Command: m.Command}
	if m.WorkspaceUID != nil {
		spec.WorkspaceUID = *m.WorkspaceUID
	}
	sink := workspacelogs.SinkOptions{Store: session.Store, Spec: spec, Acks: acks}
	if fwd != nil {
		sink.OnAccepted = fwd.Notify
	}
	built, err := workspacelogs.NewSink(sink)
	if err != nil {
		return nil
	}

	return built
}

// Options returns the tunnel server option that accepts mirrored records.
func (h *SetupLogHop) Options() []Option {
	if h == nil || h.sink == nil {
		return nil
	}

	return []Option{WithSetupLogSink(h.sink)}
}

// Flush drains everything upstream within the senders' own cleanup budget,
// even when ctx is already canceled. It reports whether the next host holds
// everything. Call it after the session is finished and before the result is
// reported.
func (h *SetupLogHop) Flush(ctx context.Context) bool {
	if h == nil {
		return true
	}
	complete := true
	if h.sender != nil {
		h.sender.Notify()
		complete = h.sender.Flush(ctx) && complete
	}
	if h.forwarder != nil {
		complete = h.forwarder.Flush(ctx) && complete
	}

	return complete
}

// Close stops the background senders.
func (h *SetupLogHop) Close() {
	if h == nil {
		return
	}
	if h.sender != nil {
		h.sender.Close()
	}
	if h.forwarder != nil {
		h.forwarder.Close()
	}
}

type hopOptionsKey struct{}

// WithSetupLogOptions carries tunnel server options for setup history to code
// that starts the server several calls down.
func WithSetupLogOptions(ctx context.Context, opts []Option) context.Context {
	if len(opts) == 0 {
		return ctx
	}

	return context.WithValue(ctx, hopOptionsKey{}, opts)
}

// SetupLogOptionsFrom returns the options stored by WithSetupLogOptions.
func SetupLogOptionsFrom(ctx context.Context) []Option {
	opts, _ := ctx.Value(hopOptionsKey{}).([]Option)

	return opts
}
