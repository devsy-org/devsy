package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func newEchoServer(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("echo listener: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				data, _ := io.ReadAll(c)
				_, _ = c.Write(data)
			}(conn)
		}
	}()

	return listener
}

func echoDialFunc(addr string) DialFunc {
	return func(ctx context.Context) (io.ReadWriteCloser, error) {
		return net.Dial("tcp", addr)
	}
}

func sendAndReceive(t *testing.T, addr string, msg []byte) []byte {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatalf("dial tunnel: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.Write(msg); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := conn.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatalf("close write: %v", err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}

	buf, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	return buf
}

// listenerDialClosed reports whether a dial error means no live listener
// remains: refused, or reset, which macOS returns during the shutdown race.
func listenerDialClosed(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET)
}

func waitForListenerClosed(t *testing.T, addr string) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error

	for {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			_ = conn.Close()
		} else if listenerDialClosed(err) {
			return
		} else if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			lastErr = err
		} else {
			t.Fatalf("listener dial failed before closure: %v", err)
		}

		select {
		case <-deadline.C:
			t.Fatalf("listener did not close before deadline; last dial error: %v", lastErr)
		case <-ticker.C:
		}
	}
}

func TestLocalTunnel_ListensOnPort(t *testing.T) {
	ctx := t.Context()

	tun, err := NewLocalTunnel(ctx, LocalTunnelOptions{
		BasePort: 18000,
		DialFunc: func(ctx context.Context) (io.ReadWriteCloser, error) {
			return nil, io.EOF
		},
	})
	if err != nil {
		t.Fatalf("NewLocalTunnel: %v", err)
	}
	defer func() { _ = tun.Close() }()

	if tun.Port() < 18000 {
		t.Errorf("expected port >= 18000, got %d", tun.Port())
	}

	conn, err := net.DialTimeout("tcp", tun.Addr(), time.Second)
	if err != nil {
		t.Fatalf("dial tunnel: %v", err)
	}
	_ = conn.Close()
}

func TestLocalTunnel_ForwardsData(t *testing.T) {
	ctx := t.Context()
	echoServer := newEchoServer(t)

	tun, err := NewLocalTunnel(ctx, LocalTunnelOptions{
		BasePort: 18100,
		DialFunc: echoDialFunc(echoServer.Addr().String()),
	})
	if err != nil {
		t.Fatalf("NewLocalTunnel: %v", err)
	}
	defer func() { _ = tun.Close() }()

	msg := []byte("hello tunnel")
	buf := sendAndReceive(t, tun.Addr(), msg)

	if string(buf) != "hello tunnel" {
		t.Errorf("expected %q, got %q", "hello tunnel", string(buf))
	}
}

func TestLocalTunnel_MultipleConcurrentConnections(t *testing.T) {
	ctx := t.Context()
	echoServer := newEchoServer(t)

	tun, err := NewLocalTunnel(ctx, LocalTunnelOptions{
		BasePort: 18200,
		DialFunc: echoDialFunc(echoServer.Addr().String()),
	})
	if err != nil {
		t.Fatalf("NewLocalTunnel: %v", err)
	}
	defer func() { _ = tun.Close() }()

	var wg sync.WaitGroup
	for i := range 5 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			msg := fmt.Appendf(nil, "msg-%d", id)
			buf := sendAndReceive(t, tun.Addr(), msg)
			if string(buf) != string(msg) {
				t.Errorf("conn %d: expected %q, got %q", id, msg, buf)
			}
		}(i)
	}
	wg.Wait()
}

func TestLocalTunnel_CloseStopsAccepting(t *testing.T) {
	ctx := t.Context()

	tun, err := NewLocalTunnel(ctx, LocalTunnelOptions{
		BasePort: 18300,
		DialFunc: func(ctx context.Context) (io.ReadWriteCloser, error) {
			return nil, io.EOF
		},
	})
	if err != nil {
		t.Fatalf("NewLocalTunnel: %v", err)
	}

	addr := tun.Addr()
	_ = tun.Close()
	waitForListenerClosed(t, addr)
}

func TestLocalTunnel_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	tun, err := NewLocalTunnel(ctx, LocalTunnelOptions{
		BasePort: 18400,
		DialFunc: func(ctx context.Context) (io.ReadWriteCloser, error) {
			return nil, io.EOF
		},
	})
	if err != nil {
		t.Fatalf("NewLocalTunnel: %v", err)
	}
	defer func() { _ = tun.Close() }()

	addr := tun.Addr()
	cancel()

	// The listener is closed asynchronously after cancellation.
	waitForListenerClosed(t, addr)
}

func TestLocalTunnel_HealthCheckShutdown(t *testing.T) {
	ctx := t.Context()

	tun, err := NewLocalTunnel(ctx, LocalTunnelOptions{
		BasePort: 18500,
		DialFunc: func(ctx context.Context) (io.ReadWriteCloser, error) {
			return nil, fmt.Errorf("workspace gone")
		},
		HealthCheckFunc: func(context.Context) error {
			return fmt.Errorf("workspace gone")
		},
		HealthCheckInterval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewLocalTunnel: %v", err)
	}
	defer func() { _ = tun.Close() }()

	// The health check should shut down the tunnel after 3 failures.
	waitForListenerClosed(t, tun.Addr())
}

func TestLocalTunnel_HealthCheckUsesHealthFuncNotDialFunc(t *testing.T) {
	ctx := t.Context()

	// DialFunc always succeeds: if the health loop still probed through the
	// data path, failures would never accumulate and the tunnel would stay
	// alive. Only a health loop driven by the failing HealthCheckFunc shuts
	// the listener down.
	var healthCalls atomic.Int32
	tun, err := NewLocalTunnel(ctx, LocalTunnelOptions{
		BasePort: 18600,
		DialFunc: func(ctx context.Context) (io.ReadWriteCloser, error) {
			local, remote := net.Pipe()
			_ = local.Close()
			return remote, nil
		},
		HealthCheckFunc: func(context.Context) error {
			healthCalls.Add(1)
			return fmt.Errorf("workspace gone")
		},
		HealthCheckInterval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewLocalTunnel: %v", err)
	}
	defer func() { _ = tun.Close() }()

	waitForListenerClosed(t, tun.Addr())
	if got := healthCalls.Load(); got < 3 {
		t.Errorf("health probe invoked %d times, want at least 3", got)
	}
}

func TestLocalTunnel_AcceptedConnectionStillUsesDialFunc(t *testing.T) {
	ctx := t.Context()
	echoServer := newEchoServer(t)

	var dialCalls atomic.Int32
	tun, err := NewLocalTunnel(ctx, LocalTunnelOptions{
		BasePort: 18700,
		DialFunc: func(ctx context.Context) (io.ReadWriteCloser, error) {
			dialCalls.Add(1)
			return net.Dial("tcp", echoServer.Addr().String())
		},
		HealthCheckFunc: func(context.Context) error {
			return nil
		},
		HealthCheckInterval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewLocalTunnel: %v", err)
	}
	defer func() { _ = tun.Close() }()

	msg := []byte("data plane")
	if buf := sendAndReceive(t, tun.Addr(), msg); string(buf) != string(msg) {
		t.Errorf("expected %q, got %q", msg, buf)
	}
	if got := dialCalls.Load(); got == 0 {
		t.Error("accepted connection did not invoke DialFunc")
	}
}

func TestLocalTunnel_HealthyStatusKeepsTunnelAlive(t *testing.T) {
	ctx := t.Context()

	tun, err := NewLocalTunnel(ctx, LocalTunnelOptions{
		BasePort: 18800,
		DialFunc: func(ctx context.Context) (io.ReadWriteCloser, error) {
			return nil, io.EOF
		},
		HealthCheckFunc: func(context.Context) error {
			return nil
		},
		HealthCheckInterval: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewLocalTunnel: %v", err)
	}
	defer func() { _ = tun.Close() }()

	// Well past the failure window, a healthy probe keeps the listener up.
	time.Sleep(200 * time.Millisecond)
	conn, err := net.DialTimeout("tcp", tun.Addr(), time.Second)
	if err != nil {
		t.Fatalf("listener closed despite healthy probes: %v", err)
	}
	_ = conn.Close()
}
