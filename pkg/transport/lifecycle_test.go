package transport

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

type testManagedConn struct {
	net.Conn
	waitErr  error
	waitDone chan struct{}
	closed   chan struct{}
}

func newTestManagedConn(waitErr error) *testManagedConn {
	conn := &testManagedConn{
		Conn:     &stubConn{},
		waitErr:  waitErr,
		waitDone: make(chan struct{}),
		closed:   make(chan struct{}),
	}
	if waitErr != nil {
		close(conn.waitDone)
	}
	return conn
}

func (c *testManagedConn) Wait() error {
	<-c.waitDone
	return c.waitErr
}

func (c *testManagedConn) Close() error {
	select {
	case <-c.closed:
	default:
		close(c.closed)
		select {
		case <-c.waitDone:
		default:
			close(c.waitDone)
		}
	}
	return nil
}

func TestRunManagedReturnsHandlerError(t *testing.T) {
	wantErr := errors.New("handler failed")
	conn := newTestManagedConn(nil)
	err := RunManaged(RunManagedOptions{
		Parent: context.Background(),
		Conn:   conn,
		Handler: func(context.Context) error {
			return wantErr
		},
		TransportSide: SideProvider,
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("RunManaged() = %v, want %v", err, wantErr)
	}
	select {
	case <-conn.closed:
	default:
		t.Fatal("connection was not closed")
	}
}

func TestRunManagedReturnsTransportError(t *testing.T) {
	wantErr := errors.New("provider failed")
	conn := newTestManagedConn(wantErr)
	err := RunManaged(RunManagedOptions{
		Parent: context.Background(),
		Conn:   conn,
		Handler: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
		TransportSide: SideProvider,
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("RunManaged() = %v, want %v", err, wantErr)
	}
}

func TestRunManagedReturnsParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	conn := newTestManagedConn(nil)
	done := make(chan error, 1)
	go func() {
		done <- RunManaged(RunManagedOptions{
			Parent: ctx,
			Conn:   conn,
			Handler: func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			},
			TransportSide: SideProvider,
		})
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("RunManaged() = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("RunManaged did not stop after cancellation")
	}
}

type stubConn struct{}

func (*stubConn) Read([]byte) (int, error)         { return 0, net.ErrClosed }
func (*stubConn) Write([]byte) (int, error)        { return 0, net.ErrClosed }
func (*stubConn) Close() error                     { return nil }
func (*stubConn) LocalAddr() net.Addr              { return Addr{} }
func (*stubConn) RemoteAddr() net.Addr             { return Addr{} }
func (*stubConn) SetDeadline(time.Time) error      { return nil }
func (*stubConn) SetReadDeadline(time.Time) error  { return nil }
func (*stubConn) SetWriteDeadline(time.Time) error { return nil }
