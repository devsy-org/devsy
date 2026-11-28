package sidecar

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/devsy-org/devsy/pkg/pool/egress"
	"github.com/devsy-org/devsy/pkg/pool/egress/channel"
)

// Options holds what a caller may add to a Config. The zero value is what
// the command uses.
type Options struct {
	Logger *slog.Logger
	// Listening is called with the proxy address once it is bound, before
	// the pool is told the proxy is ready.
	Listening func(net.Addr)
	// WrapRecorder wraps the recorder the proxy reports to. Tests use it to
	// hold a decision in flight.
	WrapRecorder func(egress.Recorder) egress.Recorder
}

// Run serves the proxy until ctx ends and returns nil after a requested stop.
//
// It binds the proxy first and only then connects to the pool, so the ready
// line means the proxy is listening. On stop the proxy ends first and drains
// its handlers, and only then is the connection to the pool closed. Closing
// it earlier would drop the decisions of requests still in flight.
func Run(ctx context.Context, cfg Config, opts Options) error {
	proxy, sender, err := assemble(cfg, opts)
	if err != nil {
		return err
	}
	addr, err := proxy.Listen()
	if err != nil {
		return err
	}
	if opts.Listening != nil {
		opts.Listening(addr)
	}
	senderCtx, stopSender := context.WithCancel(context.WithoutCancel(ctx))
	senderDone := make(chan error, 1)
	go func() { senderDone <- sender.Run(senderCtx) }()
	serveErr := proxy.Serve(ctx)
	stopSender()
	<-senderDone
	return serveErr
}

func assemble(cfg Config, opts Options) (*egress.Proxy, *channel.Sender, error) {
	policy, err := cfg.policy()
	if err != nil {
		return nil, nil, err
	}
	sender, err := channel.NewSender(channel.SenderConfig{
		Path:   cfg.EventsSocket,
		Logger: opts.Logger,
	})
	if err != nil {
		return nil, nil, err
	}
	var rec egress.Recorder = sender
	if opts.WrapRecorder != nil {
		rec = opts.WrapRecorder(sender)
	}
	proxy, err := egress.NewProxy(egress.Config{
		Bind:        cfg.Bind,
		SessionID:   cfg.SessionID,
		Dialer:      egress.NewDialer(policy),
		Recorder:    rec,
		Logger:      opts.Logger,
		Healthy:     sender.Healthy,
		MaxConns:    cfg.MaxConns,
		IdleTimeout: time.Duration(cfg.IdleSeconds) * time.Second,
		MaxLifetime: time.Duration(cfg.LifetimeSeconds) * time.Second,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("egress proxy: %w", err)
	}
	return proxy, sender, nil
}
