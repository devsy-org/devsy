package channel

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/devsy-org/devsy/pkg/pool/egress"
)

const (
	dialWait     = 2 * time.Second
	writeWait    = 2 * time.Second
	retryEvery   = time.Second
	maxFailures  = 2
	defaultRetry = retryEvery
)

// SenderStats counts what a Sender did with decisions.
type SenderStats struct {
	// Sent counts lines written to the socket.
	Sent int
	// Dropped counts decisions not delivered: a failed send, a send while
	// unhealthy, or a decision that cannot be encoded.
	Dropped int
}

// SenderConfig sets a Sender.
type SenderConfig struct {
	// Path is the session socket the pool listens on.
	Path string
	// Retry is the wait between reconnect attempts. Zero means one second.
	Retry  time.Duration
	Logger *slog.Logger
}

// Sender is the sidecar's egress.Recorder. It writes each decision to the
// pool and tracks whether the pool can be reached. Healthy turns false after
// two sends in a row fail, and the proxy then refuses new requests, because
// a decision the pool cannot record must not be acted on. Run brings it back
// when the socket answers again.
type Sender struct {
	cfg SenderConfig

	// healthy is read by every proxy request, so it never waits on mu, which
	// is held across a dial or a write.
	healthy atomic.Bool

	mu       sync.Mutex
	conn     net.Conn
	failures int
	stats    SenderStats
}

var _ egress.Recorder = (*Sender)(nil)

// NewSender checks the config. The Sender is not healthy until Run has
// connected.
func NewSender(cfg SenderConfig) (*Sender, error) {
	if cfg.Path == "" {
		return nil, errors.New("egress channel sender needs a socket path")
	}
	if cfg.Retry < 0 {
		return nil, errors.New("egress channel retry must not be negative")
	}
	if cfg.Retry == 0 {
		cfg.Retry = defaultRetry
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return &Sender{cfg: cfg}, nil
}

// Healthy reports whether decisions are reaching the pool.
func (s *Sender) Healthy() bool { return s.healthy.Load() }

// Stats returns a copy of the counters.
func (s *Sender) Stats() SenderStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

// Denied sends a refusal to the pool.
func (s *Sender) Denied(d egress.Denial) {
	s.send(EncodeDenial(d))
}

// Allowed sends an allowed tunnel to the pool.
func (s *Sender) Allowed(a egress.Allow) {
	s.send(EncodeAllow(a))
}

// Run connects, sends the ready line, and reconnects whenever the Sender is
// unhealthy, until ctx ends. It closes the connection on return.
func (s *Sender) Run(ctx context.Context) error {
	defer s.close()
	for {
		if !s.Healthy() {
			s.reconnect(ctx)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(s.cfg.Retry):
		}
	}
}

// reconnect dials without holding mu, so requests are not held up while the
// pool is unreachable.
func (s *Sender) reconnect(ctx context.Context) {
	conn, err := s.dial(ctx)
	if err != nil {
		s.cfg.Logger.Warn("egress channel not reachable", "error", err.Error())
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeLocked()
	s.conn = conn
	if err := s.writeConnLocked(encodeReady()); err != nil {
		s.closeLocked()
		s.cfg.Logger.Warn("egress channel ready line failed", "error", err.Error())
		return
	}
	s.failures = 0
	s.healthy.Store(true)
}

func (s *Sender) dial(ctx context.Context) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, dialWait)
	defer cancel()
	return (&net.Dialer{}).DialContext(ctx, "unix", s.cfg.Path)
}

func (s *Sender) send(line []byte, encodeErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if encodeErr != nil || !s.healthy.Load() {
		s.stats.Dropped++
		return
	}
	if err := s.writeLocked(line); err != nil {
		s.stats.Dropped++
		s.failures++
		if s.failures >= maxFailures {
			s.healthy.Store(false)
		}
		s.cfg.Logger.Warn("egress channel send failed", "error", err.Error())
		return
	}
	s.failures = 0
	s.stats.Sent++
}

// writeLocked writes one line, connecting first if there is no connection.
// A write that fails on a connection that was already open usually means the
// pool restarted, so the line is sent once more on a new connection before it
// counts as a failure.
func (s *Sender) writeLocked(line []byte) error {
	reused := s.conn != nil
	if !reused {
		if err := s.connectLocked(); err != nil {
			return err
		}
	}
	err := s.writeConnLocked(line)
	if err == nil {
		return nil
	}
	s.closeLocked()
	if !reused {
		return err
	}
	return s.resendLocked(line)
}

func (s *Sender) resendLocked(line []byte) error {
	if err := s.connectLocked(); err != nil {
		return err
	}
	if err := s.writeConnLocked(line); err != nil {
		s.closeLocked()
		return err
	}
	return nil
}

// connectLocked is the second chance after a failed send: it dials with mu
// held, for at most dialWait.
func (s *Sender) connectLocked() error {
	conn, err := s.dial(context.Background())
	if err != nil {
		return err
	}
	s.conn = conn
	if err := s.writeConnLocked(encodeReady()); err != nil {
		s.closeLocked()
		return err
	}
	return nil
}

func (s *Sender) writeConnLocked(line []byte) error {
	if err := s.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
		return err
	}
	_, err := s.conn.Write(line)
	return err
}

func (s *Sender) close() {
	s.healthy.Store(false)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeLocked()
}

func (s *Sender) closeLocked() {
	if s.conn != nil {
		_ = s.conn.Close()
		s.conn = nil
	}
}
