package ssh

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/devsy-org/devsy/pkg/transport"
	xssh "golang.org/x/crypto/ssh"
)

// ManagedClientOptions configures SSH over a transport whose peer may still be starting.
// Zero timeout values use the standard SSH handshake limits.
type ManagedClientOptions struct {
	User                 string
	KeyBytes             []byte
	HandshakeIdleTimeout time.Duration
	HandshakeMaxTimeout  time.Duration
}

type managedHandshakeConn struct {
	net.Conn
	tracking    atomic.Bool
	peerStarted atomic.Bool
	peerOnce    sync.Once
	peerReady   chan struct{}
	activity    atomic.Pointer[time.Time]
}

type managedSSHPhase uint8

const (
	managedSSHBootstrap managedSSHPhase = iota
	managedSSHHandshake
)

func (c *managedHandshakeConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.recordActivity()
		c.peerOnce.Do(func() {
			c.peerStarted.Store(true)
			close(c.peerReady)
		})
	}
	return n, err
}

func (c *managedHandshakeConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 && c.peerStarted.Load() {
		c.recordActivity()
	}
	return n, err
}

func (c *managedHandshakeConn) recordActivity() {
	if c.tracking.Load() {
		now := time.Now()
		c.activity.Store(&now)
	}
}

// ClientFromManagedConn starts the SSH client immediately, allowing its local
// identification write during bootstrap. Handshake limits begin only when the
// managed peer first sends protocol bytes.
func ClientFromManagedConn(
	ctx context.Context,
	conn transport.ManagedConn,
	opts ManagedClientOptions,
) (*xssh.Client, error) {
	if conn == nil {
		return nil, errors.New("managed connection is required")
	}
	if ctx == nil {
		return nil, errors.New("context is required")
	}
	if err := ctx.Err(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	config, err := ConfigFromKeyBytes(opts.KeyBytes)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	config.User = opts.User

	idle := opts.HandshakeIdleTimeout
	if idle <= 0 {
		idle = HandshakeIdleTimeout
	}
	maxTimeout := opts.HandshakeMaxTimeout
	if maxTimeout <= 0 {
		maxTimeout = handshakeMaxTimeout
	}

	dialer := newManagedSSHClientDialer(managedSSHClientOptions{
		ctx: ctx, conn: conn, config: config, idle: idle, maxTimeout: maxTimeout,
	})
	return dialer.run()
}

type managedSSHClientOptions struct {
	ctx        context.Context
	conn       transport.ManagedConn
	config     *xssh.ClientConfig
	idle       time.Duration
	maxTimeout time.Duration
}

type managedSSHClientDialer struct {
	ctx           context.Context
	conn          transport.ManagedConn
	tracked       *managedHandshakeConn
	result        chan handshakeResult
	transportDone chan error
	phase         managedSSHPhase
	idle          time.Duration
	maxTimeout    time.Duration
	ticker        *time.Ticker
	maxTimer      *time.Timer
	peerReady     <-chan struct{}
	ticks         <-chan time.Time
	maximum       <-chan time.Time
}

func newManagedSSHClientDialer(opts managedSSHClientOptions) *managedSSHClientDialer {
	ctx, conn, config := opts.ctx, opts.conn, opts.config
	tracked := &managedHandshakeConn{Conn: conn, peerReady: make(chan struct{})}
	tracked.tracking.Store(true)
	dialer := &managedSSHClientDialer{
		ctx: ctx, conn: conn, tracked: tracked, result: make(chan handshakeResult, 1),
		transportDone: make(chan error, 1), phase: managedSSHBootstrap,
		idle: opts.idle, maxTimeout: opts.maxTimeout, peerReady: tracked.peerReady,
	}
	go func() {
		c, chans, reqs, err := xssh.NewClientConn(tracked, "stdio", config)
		dialer.result <- handshakeResult{c: c, chans: chans, reqs: reqs, err: err}
	}()
	go func() { dialer.transportDone <- conn.Wait() }()
	return dialer
}

func (d *managedSSHClientDialer) run() (*xssh.Client, error) {
	defer d.stop()
	for {
		client, err, done := d.waitEvent()
		if done {
			return client, err
		}
	}
}

func (d *managedSSHClientDialer) waitEvent() (*xssh.Client, error, bool) {
	select {
	case <-d.peerReady:
		d.startHandshakeTimers()
		return nil, nil, false
	case res := <-d.result:
		client, err := d.finishHandshake(res)
		return client, err, true
	case transportErr := <-d.transportDone:
		client, err := d.finishTransport(transportErr)
		return client, err, true
	case <-d.ctx.Done():
		return nil, d.finishCancellation(), true
	case <-d.ticks:
		return d.checkIdleTimeout()
	case <-d.maximum:
		client, err := d.finishTimeout(d.maxTimeout)
		return client, err, true
	}
}

func (d *managedSSHClientDialer) startHandshakeTimers() {
	d.peerReady = nil
	d.phase = managedSSHHandshake
	tickInterval := d.idle / 4
	if tickInterval <= 0 {
		tickInterval = d.idle
	}
	d.ticker = time.NewTicker(tickInterval)
	d.ticks = d.ticker.C
	d.maxTimer = time.NewTimer(d.maxTimeout)
	d.maximum = d.maxTimer.C
}

func (d *managedSSHClientDialer) stop() {
	d.tracked.tracking.Store(false)
	if d.ticker != nil {
		d.ticker.Stop()
	}
	if d.maxTimer != nil {
		d.maxTimer.Stop()
	}
}

func (d *managedSSHClientDialer) finishHandshake(res handshakeResult) (*xssh.Client, error) {
	if res.err == nil {
		return d.newClient(res)
	}
	if !d.tracked.peerStarted.Load() || isManagedStreamEnd(res.err) {
		// A read reset before peer output can race the callback's terminal
		// bootstrap error. The managed result is authoritative in this phase.
		res.err = managedTerminalError(d.ctx, d.transportDone)
	}
	d.closeConn()
	return nil, res.err
}

func (d *managedSSHClientDialer) finishTransport(transportErr error) (*xssh.Client, error) {
	if d.tracked.peerStarted.Load() {
		d.phase = managedSSHHandshake
	}
	// A completed launcher cannot produce more protocol bytes. Check a
	// completed handshake first so a concrete SSH error is preserved.
	select {
	case res := <-d.result:
		if res.err == nil {
			return d.newClient(res)
		}
		if d.phase == managedSSHHandshake && !isManagedStreamEnd(res.err) {
			d.closeConn()
			return nil, res.err
		}
	default:
	}
	d.closeConn()
	if transportErr == nil {
		return nil, io.EOF
	}
	return nil, transportErr
}

func (d *managedSSHClientDialer) finishCancellation() error {
	if err := d.pendingError(); err != nil {
		d.closeConn()
		return err
	}
	d.closeConn()
	return d.ctx.Err()
}

func (d *managedSSHClientDialer) pendingError() error {
	if err := d.pendingHandshakeError(); err != nil {
		return err
	}
	return d.pendingTransportError()
}

func (d *managedSSHClientDialer) pendingHandshakeError() error {
	if d.phase != managedSSHHandshake {
		return nil
	}
	select {
	case res := <-d.result:
		if res.err != nil && !isManagedStreamEnd(res.err) {
			return res.err
		}
	default:
	}
	return nil
}

func (d *managedSSHClientDialer) pendingTransportError() error {
	select {
	case transportErr := <-d.transportDone:
		if transportErr != nil && !errors.Is(transportErr, d.ctx.Err()) {
			return transportErr
		}
	default:
	}
	return nil
}

func (d *managedSSHClientDialer) checkIdleTimeout() (*xssh.Client, error, bool) {
	activity := d.tracked.activity.Load()
	if activity == nil || time.Since(*activity) <= d.idle {
		return nil, nil, false
	}
	client, err := d.finishTimeout(d.idle)
	return client, err, true
}

func (d *managedSSHClientDialer) finishTimeout(timeout time.Duration) (*xssh.Client, error) {
	if err := d.ctx.Err(); err != nil {
		d.closeConn()
		return nil, err
	}
	select {
	case res := <-d.result:
		if res.err == nil {
			return d.newClient(res)
		}
		if isManagedStreamEnd(res.err) {
			res.err = managedTerminalError(d.ctx, d.transportDone)
		}
		d.closeConn()
		return nil, res.err
	case transportErr := <-d.transportDone:
		d.closeConn()
		if transportErr != nil {
			return nil, transportErr
		}
		return nil, io.EOF
	default:
	}
	d.closeConn()
	return nil, &HandshakeTimeoutError{idle: timeout}
}

func (d *managedSSHClientDialer) newClient(res handshakeResult) (*xssh.Client, error) {
	if err := d.ctx.Err(); err != nil {
		d.closeConn()
		return nil, err
	}
	return xssh.NewClient(res.c, res.chans, handleKeepAliveRequests(res.reqs)), nil
}

func (d *managedSSHClientDialer) closeConn() {
	_ = d.conn.Close()
}

func managedTerminalError(ctx context.Context, transportDone <-chan error) error {
	select {
	case err := <-transportDone:
		if err != nil {
			return err
		}
		return io.EOF
	case <-ctx.Done():
		select {
		case err := <-transportDone:
			if err != nil && !errors.Is(err, ctx.Err()) {
				return err
			}
		default:
		}
		return ctx.Err()
	}
}

func isManagedStreamEnd(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, io.ErrClosedPipe) || errors.Is(err, net.ErrClosed)
}
