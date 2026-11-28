package egress

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

const copyBufSize = 32 << 10

// tunnelConns holds the two sides of one tunnel. clientIn replays the bytes
// http already buffered and then reads the connection itself. The http
// server's own reader cancels the request context on a read deadline, which
// would end a tunnel that is only quiet.
type tunnelConns struct {
	client   net.Conn
	clientIn io.Reader
	up       net.Conn
}

func (t tunnelConns) close() {
	_ = t.client.Close()
	_ = t.up.Close()
}

func hijack(w http.ResponseWriter) (net.Conn, io.Reader, error) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response does not support hijack")
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return nil, nil, err
	}
	in := rw.Reader
	buffered, err := in.Peek(in.Buffered())
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	return conn, io.MultiReader(bytes.NewReader(bytes.Clone(buffered)), conn), nil
}

// tunnel answers 200, requires a ClientHello for the CONNECT host, forwards it
// and then copies bytes both ways until either side ends, the tunnel idles or
// ctx ends. Nothing is sent upstream before the hello has passed.
func (p *Proxy) tunnel(ctx context.Context, t tunnelConns, req connectRequest) {
	ctx, cancel := context.WithTimeout(ctx, p.lifetime)
	defer cancel()
	defer t.close()
	stop := context.AfterFunc(ctx, t.close)
	defer stop()
	if !p.open(t, req) {
		return
	}
	p.rec.Allowed(Allow{Target: req.target, Rule: req.rule})
	t.splice(p.idle)
}

func (p *Proxy) open(t tunnelConns, req connectRequest) bool {
	deadline := time.Now().Add(p.helloTimeout)
	if err := t.client.SetDeadline(deadline); err != nil {
		return false
	}
	if _, err := io.WriteString(t.client, connectEstablished); err != nil {
		return false
	}
	hello, reason := readClientHello(t.clientIn)
	if reason == "" && hello.name != req.host {
		reason = SNIMismatch
	}
	if reason != "" {
		p.recordDenied(Denial{Target: req.target, Reason: reason})
		return false
	}
	return p.sendHello(t.up, hello, deadline, req)
}

func (p *Proxy) sendHello(
	up net.Conn,
	hello clientHello,
	deadline time.Time,
	req connectRequest,
) bool {
	if err := up.SetWriteDeadline(deadline); err != nil {
		return false
	}
	if _, err := up.Write(hello.record); err != nil {
		p.log.Warn("egress hello forward failed",
			"host", req.target.Host, "port", req.port, "error", err.Error())
		return false
	}
	return true
}

// relay tracks activity in both directions, so a one-way stream that never
// gets a reply is not idle.
type relay struct {
	idle time.Duration
	last atomic.Int64
}

func (r *relay) touch() { r.last.Store(time.Now().UnixNano()) }

func (r *relay) quiet() bool {
	return time.Since(time.Unix(0, r.last.Load())) >= r.idle
}

func (t tunnelConns) splice(idle time.Duration) {
	r := &relay{idle: idle}
	r.touch()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		t.forward(t.up, t.clientIn, t.client, r)
	}()
	go func() {
		defer wg.Done()
		t.forward(t.client, t.up, t.up, r)
	}()
	wg.Wait()
}

// forward copies one direction. A clean end closes only the write side of the
// destination, so the other direction can finish. Any other end closes both.
func (t tunnelConns) forward(dst net.Conn, src io.Reader, srcConn net.Conn, r *relay) {
	err := r.copy(dst, src, srcConn)
	if errors.Is(err, io.EOF) {
		closeWrite(dst)
		return
	}
	t.close()
}

func (r *relay) copy(dst net.Conn, src io.Reader, srcConn net.Conn) error {
	buf := make([]byte, copyBufSize)
	for {
		_ = srcConn.SetReadDeadline(time.Now().Add(r.idle))
		n, err := src.Read(buf)
		if n > 0 {
			r.touch()
			_ = dst.SetWriteDeadline(time.Now().Add(r.idle))
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return werr
			}
		}
		if err == nil || (errors.Is(err, os.ErrDeadlineExceeded) && !r.quiet()) {
			continue
		}
		return err
	}
}

type halfCloser interface{ CloseWrite() error }

func closeWrite(c net.Conn) {
	if hc, ok := c.(halfCloser); ok {
		_ = hc.CloseWrite()
		return
	}
	_ = c.Close()
}

// limitListener holds at most cap connections open. Accept waits for a free
// slot, so a flood queues in the kernel backlog instead of in memory.
type limitListener struct {
	net.Listener
	slots  chan struct{}
	closed chan struct{}
	once   sync.Once
}

func newLimitListener(ln net.Listener, limit int) *limitListener {
	return &limitListener{
		Listener: ln,
		slots:    make(chan struct{}, limit),
		closed:   make(chan struct{}),
	}
}

func (l *limitListener) Accept() (net.Conn, error) {
	select {
	case l.slots <- struct{}{}:
	case <-l.closed:
		return nil, net.ErrClosed
	}
	conn, err := l.Listener.Accept()
	if err != nil {
		<-l.slots
		return nil, err
	}
	return &limitConn{Conn: conn, release: func() { <-l.slots }}, nil
}

func (l *limitListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return l.Listener.Close()
}

// limitConn frees its slot once, whichever side closes it first.
type limitConn struct {
	net.Conn
	release func()
	once    sync.Once
}

func (c *limitConn) Close() error {
	c.once.Do(c.release)
	return c.Conn.Close()
}

func (c *limitConn) CloseWrite() error {
	if hc, ok := c.Conn.(halfCloser); ok {
		return hc.CloseWrite()
	}
	return c.Close()
}
