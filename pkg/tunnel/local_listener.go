package tunnel

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/port"
)

// LocalTunnel manages a local TCP listener that forwards connections
// to a container SSH server through the Devsy tunnel infrastructure.
type LocalTunnel struct {
	listener net.Listener
	port     int
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	dialFunc DialFunc
}

// DialFunc establishes a connection to the remote SSH server.
// Each call returns a new bidirectional stream to the container SSH port.
type DialFunc func(ctx context.Context) (io.ReadWriteCloser, error)

// LocalTunnelOptions configures a local TCP tunnel.
type LocalTunnelOptions struct {
	// BasePort is the starting port to search from (default: 10800)
	BasePort int
	// DialFunc creates a connection to the remote SSH endpoint
	DialFunc DialFunc
}

// NewLocalTunnel creates and starts a local TCP listener that forwards
// connections to the container SSH server.
func NewLocalTunnel(ctx context.Context, opts LocalTunnelOptions) (*LocalTunnel, error) {
	if opts.DialFunc == nil {
		return nil, fmt.Errorf("dial func is required")
	}
	if opts.BasePort == 0 {
		opts.BasePort = 10800
	}

	availablePort, err := port.FindAvailablePort(opts.BasePort)
	if err != nil {
		return nil, fmt.Errorf("find available port: %w", err)
	}

	addr := fmt.Sprintf("127.0.0.1:%d", availablePort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", addr, err)
	}

	tunnelCtx, cancel := context.WithCancel(ctx)
	t := &LocalTunnel{
		listener: listener,
		port:     availablePort,
		ctx:      tunnelCtx,
		cancel:   cancel,
		dialFunc: opts.DialFunc,
	}

	t.wg.Add(1)
	go t.acceptLoop()

	go func() {
		<-tunnelCtx.Done()
		_ = listener.Close()
	}()

	return t, nil
}

// Port returns the local port the tunnel is listening on.
func (t *LocalTunnel) Port() int {
	return t.port
}

// Addr returns the full listener address (e.g., "127.0.0.1:10800").
func (t *LocalTunnel) Addr() string {
	return t.listener.Addr().String()
}

// Close shuts down the tunnel listener and waits for active connections to finish.
func (t *LocalTunnel) Close() error {
	t.cancel()
	err := t.listener.Close()
	t.wg.Wait()
	return err
}

func (t *LocalTunnel) acceptLoop() {
	defer t.wg.Done()

	for {
		conn, err := t.listener.Accept()
		if err != nil {
			if t.ctx.Err() != nil {
				return
			}
			log.Debugf("tunnel accept error: %v", err)
			return
		}

		t.wg.Add(1)
		go t.handleConnection(conn)
	}
}

func (t *LocalTunnel) handleConnection(localConn net.Conn) {
	defer t.wg.Done()
	defer func() { _ = localConn.Close() }()

	remoteConn, err := t.dialFunc(t.ctx)
	if err != nil {
		log.Debugf("tunnel dial error: %v", err)
		return
	}
	defer func() { _ = remoteConn.Close() }()

	bridgeConnections(t.ctx, localConn, remoteConn)
}

func bridgeConnections(ctx context.Context, local net.Conn, remote io.ReadWriteCloser) {
	done := make(chan struct{})
	defer close(done)

	go func() {
		select {
		case <-ctx.Done():
			_ = local.Close()
			_ = remote.Close()
		case <-done:
		}
	}()

	var wg sync.WaitGroup
	wg.Add(2)

	// local -> remote: when local's read side hits EOF, close remote's write side
	go func() {
		defer wg.Done()
		_, _ = io.Copy(remote, local)
		// Signal the remote that no more data is coming
		if tc, ok := remote.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		} else {
			// For non-TCP remote connections, close entirely to signal EOF
			_ = remote.Close()
		}
	}()

	// remote -> local: when remote's read side hits EOF, close local's write side
	go func() {
		defer wg.Done()
		_, _ = io.Copy(local, remote)
		// Signal the local client that no more data is coming
		if tc, ok := local.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
	}()

	wg.Wait()
}
