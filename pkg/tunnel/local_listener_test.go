package tunnel

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
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

	time.Sleep(50 * time.Millisecond)
	_, err = net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if err == nil {
		t.Error("expected connection to be refused after Close()")
	}
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

	time.Sleep(100 * time.Millisecond)
	_, err = net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if err == nil {
		t.Error("expected connection to be refused after context cancellation")
	}
}

func TestLocalTunnel_HealthCheckShutdown(t *testing.T) {
	ctx := t.Context()

	tun, err := NewLocalTunnel(ctx, LocalTunnelOptions{
		BasePort: 18500,
		DialFunc: func(ctx context.Context) (io.ReadWriteCloser, error) {
			return nil, fmt.Errorf("workspace gone")
		},
		HealthCheckInterval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewLocalTunnel: %v", err)
	}
	defer func() { _ = tun.Close() }()

	// The health check should shut down the tunnel after 3 failures
	// 3 * 50ms = 150ms, give generous timeout
	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			t.Fatal("tunnel did not shut down after health check failures")
		case <-ticker.C:
			conn, err := net.DialTimeout("tcp", tun.Addr(), 50*time.Millisecond)
			if err != nil {
				return // tunnel shut down - test passes
			}
			_ = conn.Close()
		}
	}
}
