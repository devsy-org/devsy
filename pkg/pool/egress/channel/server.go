package channel

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/pool/egress"
)

const (
	maxConns = 4
	// drainWait is how long Serve keeps reading after it is told to stop, so
	// lines already sent are not lost.
	drainWait = time.Second
	// maxSocketPath is below the shortest sun_path limit, 104 bytes on macOS.
	maxSocketPath = 100
	socketMode    = 0o600
	// defaultIdle closes a connection that sends nothing, so idle peers
	// cannot hold every slot. A Sender whose connection was closed this way
	// reconnects on its next send.
	defaultIdle = 5 * time.Minute
)

// Sink receives the decisions the sidecar reports. *events.Recorder is a
// Sink.
type Sink interface {
	Denied(egress.Denial)
	// Allowed reports whether the decision was counted. The server does not
	// act on it.
	Allowed(egress.Allow) bool
}

// ServerStats counts what a Server read.
type ServerStats struct {
	// Events counts lines passed to the sink.
	Events int
	// Invalid counts lines that failed the checks.
	Invalid int
	// Oversized counts lines longer than MaxLine.
	Oversized int
	// Refused counts connections closed because maxConns were open.
	Refused int
}

// Server reads one session's socket and passes each valid decision to the
// sink. It holds no state about the session beyond that sink.
type Server struct {
	sink  Sink
	log   *slog.Logger
	ready chan struct{}
	once  sync.Once
	idle  time.Duration

	mu       sync.Mutex
	stats    ServerStats
	conns    map[net.Conn]struct{}
	draining bool
}

// NewServer returns a Server for one session's sink. A nil logger discards.
func NewServer(sink Sink, logger *slog.Logger) (*Server, error) {
	if sink == nil {
		return nil, errors.New("egress channel server needs a sink")
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Server{
		sink:  sink,
		log:   logger,
		ready: make(chan struct{}),
		idle:  defaultIdle,
		conns: map[net.Conn]struct{}{},
	}, nil
}

// Listen creates the session's socket at path with mode 0600. path must not
// exist, so a stale or foreign file is never removed, and its directory must
// be owned by the pool and closed to others, so no other user can reach the
// socket before the mode is set. The caller should make that directory itself,
// with os.MkdirTemp or a 0700 Mkdir, so no ancestor another user can write to
// lets it be swapped between the check and the bind.
func Listen(path string) (net.Listener, error) {
	if len(path) > maxSocketPath {
		return nil, fmt.Errorf("egress socket path is longer than %d bytes", maxSocketPath)
	}
	if err := checkPrivateDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(path); err == nil {
		return nil, errors.New("egress socket path already exists")
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("egress socket listen: %w", err)
	}
	if err := os.Chmod(path, socketMode); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("egress socket mode: %w", err)
	}
	return ln, nil
}

// Ready is closed when the sidecar first reports that its proxy is
// listening.
func (s *Server) Ready() <-chan struct{} { return s.ready }

// Stats returns a copy of the counters.
func (s *Server) Stats() ServerStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

// Serve accepts connections on ln until ctx ends. It then stops accepting,
// gives open connections drainWait to deliver what they already sent, and
// returns once every reader has stopped. It returns nil after a requested
// stop and closes ln.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	stop := context.AfterFunc(ctx, func() { _ = ln.Close() })
	defer stop()
	var wg sync.WaitGroup
	slots := make(chan struct{}, maxConns)
	var acceptErr error
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() == nil {
				acceptErr = fmt.Errorf("egress socket accept: %w", err)
			}
			break
		}
		s.start(conn, slots, &wg)
	}
	_ = ln.Close()
	s.drain()
	wg.Wait()
	return acceptErr
}

func (s *Server) start(conn net.Conn, slots chan struct{}, wg *sync.WaitGroup) {
	select {
	case slots <- struct{}{}:
	default:
		_ = conn.Close()
		s.count(func(st *ServerStats) { st.Refused++ })
		return
	}
	s.track(conn, true)
	wg.Go(func() {
		defer func() { <-slots }()
		s.read(conn)
	})
}

func (s *Server) track(conn net.Conn, add bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if add {
		s.conns[conn] = struct{}{}
		return
	}
	delete(s.conns, conn)
}

func (s *Server) drain() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.draining = true
	deadline := time.Now().Add(drainWait)
	for conn := range s.conns {
		_ = conn.SetReadDeadline(deadline)
	}
}

func (s *Server) count(update func(*ServerStats)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	update(&s.stats)
}

func (s *Server) read(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	defer s.track(conn, false)
	r := bufio.NewReaderSize(conn, MaxLine)
	for {
		s.arm(conn)
		raw, err := r.ReadSlice('\n')
		switch {
		case err == nil:
			s.dispatch(raw)
		case errors.Is(err, bufio.ErrBufferFull):
			s.count(func(st *ServerStats) { st.Oversized++ })
			skipLine(r)
		default:
			return
		}
	}
}

// arm sets the idle deadline, unless the server is draining, which has set a
// shorter one that a read must not push back.
func (s *Server) arm(conn net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.draining {
		_ = conn.SetReadDeadline(time.Now().Add(s.idle))
	}
}

// skipLine discards the rest of an oversized line. A read error ends it,
// and the caller's next read returns the same error.
func skipLine(r *bufio.Reader) {
	for {
		if _, err := r.ReadSlice('\n'); !errors.Is(err, bufio.ErrBufferFull) {
			return
		}
	}
}

func (s *Server) dispatch(raw []byte) {
	ev, err := Decode(raw)
	if err != nil {
		s.count(func(st *ServerStats) { st.Invalid++ })
		return
	}
	switch ev.Kind {
	case KindReady:
		s.once.Do(func() { close(s.ready) })
		return
	case KindDeny:
		s.sink.Denied(ev.Denial)
	case KindAllow:
		_ = s.sink.Allowed(ev.Allow)
	}
	s.count(func(st *ServerStats) { st.Events++ })
}
