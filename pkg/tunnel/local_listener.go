package tunnel

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/log"
)

// LocalTunnel manages a local TCP listener that forwards connections
// to a container SSH server through the Devsy tunnel infrastructure.
type LocalTunnel struct {
	listener            net.Listener
	port                int
	ctx                 context.Context
	cancel              context.CancelFunc
	wg                  sync.WaitGroup
	dialFunc            DialFunc
	healthCheckInterval time.Duration
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
	// HealthCheckInterval overrides the default health check interval (for testing).
	// If zero, defaults to healthCheckInterval (30s).
	HealthCheckInterval time.Duration
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

	listener, listenPort, err := listenAvailablePort(opts.BasePort)
	if err != nil {
		return nil, err
	}

	healthInterval := opts.HealthCheckInterval
	if healthInterval == 0 {
		healthInterval = healthCheckInterval
	}

	tunnelCtx, cancel := context.WithCancel(ctx)
	t := &LocalTunnel{
		listener:            listener,
		port:                listenPort,
		ctx:                 tunnelCtx,
		cancel:              cancel,
		dialFunc:            opts.DialFunc,
		healthCheckInterval: healthInterval,
	}

	t.wg.Add(1)
	go t.acceptLoop()

	go func() {
		<-tunnelCtx.Done()
		_ = listener.Close()
	}()

	t.wg.Add(1)
	go t.healthCheck()

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

func (t *LocalTunnel) healthCheck() {
	defer t.wg.Done()

	ticker := time.NewTicker(t.healthCheckInterval)
	defer ticker.Stop()

	failures := 0
	for {
		select {
		case <-t.ctx.Done():
			return
		case <-ticker.C:
			conn, err := t.dialFunc(t.ctx)
			if err != nil {
				failures++
				log.Debugf(
					"tunnel health check failed (%d/%d): %v",
					failures,
					healthCheckMaxFailures,
					err,
				)
				if failures >= healthCheckMaxFailures {
					log.Infof(
						"tunnel health check: %d consecutive failures, shutting down",
						failures,
					)
					t.cancel()
					return
				}
			} else {
				_ = conn.Close()
				failures = 0
			}
		}
	}
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

const (
	listenPortRange        = 100
	healthCheckInterval    = 30 * time.Second
	healthCheckMaxFailures = 3
)

func listenAvailablePort(basePort int) (net.Listener, int, error) {
	for i := range listenPortRange {
		addr := fmt.Sprintf("127.0.0.1:%d", basePort+i)
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			continue
		}
		return listener, basePort + i, nil
	}
	return nil, 0, fmt.Errorf(
		"no available port in range %d-%d", basePort, basePort+listenPortRange-1,
	)
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
