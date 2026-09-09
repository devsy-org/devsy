package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
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

type controlledManagedConn struct {
	net.Conn
	mu                  sync.Mutex
	triggerOnce         sync.Once
	closeOnce           sync.Once
	waitErr             error
	triggerWait         chan struct{}
	waitResultPublished chan struct{}
	closed              chan struct{}
}

func newControlledManagedConn() *controlledManagedConn {
	return &controlledManagedConn{
		Conn:                &stubConn{},
		triggerWait:         make(chan struct{}),
		waitResultPublished: make(chan struct{}),
		closed:              make(chan struct{}),
	}
}

func (c *controlledManagedConn) Wait() error {
	<-c.triggerWait
	close(c.waitResultPublished)
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.waitErr
}

func (c *controlledManagedConn) Close() error {
	c.closeOnce.Do(func() {
		close(c.closed)
		c.triggerOnce.Do(func() { close(c.triggerWait) })
	})
	return nil
}

func (c *controlledManagedConn) TriggerCleanupError(err error) {
	c.mu.Lock()
	c.waitErr = err
	c.mu.Unlock()
	c.triggerOnce.Do(func() { close(c.triggerWait) })
}

func TestRunManagedHandlerSuccessBeatsCleanupError(t *testing.T) {
	conn := newControlledManagedConn()
	errTeardown := errors.New("wait: remote command exited without exit status or exit signal")

	err := RunManaged(RunManagedOptions{
		Parent:        context.Background(),
		Conn:          conn,
		TransportSide: SideProvider,
		Handler: func(ctx context.Context) error {
			conn.TriggerCleanupError(errTeardown)
			<-ctx.Done()
			return nil
		},
	})
	if err != nil {
		t.Fatalf("RunManaged() = %v, want nil", err)
	}
}

func TestRunManagedHandlerErrorWinsOverCleanupError(t *testing.T) {
	conn := newControlledManagedConn()
	errTeardown := errors.New("wait: remote command exited without exit status or exit signal")
	wantErr := errors.New("command exited with status 127")

	err := RunManaged(RunManagedOptions{
		Parent:        context.Background(),
		Conn:          conn,
		TransportSide: SideProvider,
		Handler: func(ctx context.Context) error {
			conn.TriggerCleanupError(errTeardown)
			<-ctx.Done()
			return wantErr
		},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("RunManaged() = %v, want %v", err, wantErr)
	}
}

func TestRunManagedProviderFailureWinsOverLaterHandlerSuccess(t *testing.T) {
	conn := newControlledManagedConn()
	providerErr := errors.New("connection reset by peer")

	err := RunManaged(RunManagedOptions{
		Parent:        context.Background(),
		Conn:          conn,
		TransportSide: SideProvider,
		Handler: func(ctx context.Context) error {
			conn.TriggerCleanupError(providerErr)
			<-ctx.Done()
			return nil
		},
	})
	if !errors.Is(err, providerErr) {
		t.Fatalf("RunManaged() = %v, want %v", err, providerErr)
	}
}

func TestRunManagedGenuineTransportFailure(t *testing.T) {
	conn := newControlledManagedConn()
	providerErr := errors.New("connection reset by peer")
	conn.TriggerCleanupError(providerErr)

	err := RunManaged(RunManagedOptions{
		Parent:        context.Background(),
		Conn:          conn,
		TransportSide: SideProvider,
		Handler: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
	})
	if !errors.Is(err, providerErr) {
		t.Fatalf("RunManaged() = %v, want %v", err, providerErr)
	}
}

func TestRunManagedStressHandlerSuccessBeatsCleanup(t *testing.T) {
	errTeardown := errors.New("wait: remote command exited without exit status or exit signal")
	for i := range 100 {
		conn := newControlledManagedConn()
		err := RunManaged(RunManagedOptions{
			Parent:        context.Background(),
			Conn:          conn,
			TransportSide: SideProvider,
			Handler: func(ctx context.Context) error {
				conn.TriggerCleanupError(errTeardown)
				<-conn.waitResultPublished
				return nil
			},
		})
		if err != nil {
			t.Fatalf("iteration %d: RunManaged() = %v, want nil", i, err)
		}
	}
}

func TestRunManagedBoundedSecondSideShutdown(t *testing.T) {
	conn := newTestManagedConn(nil)
	handlerStuck := make(chan struct{})
	defer close(handlerStuck)

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		done <- RunManaged(RunManagedOptions{
			Parent:        context.Background(),
			Conn:          conn,
			TransportSide: SideProvider,
			JoinTimeout:   50 * time.Millisecond,
			Handler: func(ctx context.Context) error {
				<-handlerStuck
				return nil
			},
		})
	}()

	_ = conn.Close()

	select {
	case <-done:
		if time.Since(start) > 2*time.Second {
			t.Fatal("RunManaged took too long to return after bounded join timeout")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunManaged hung waiting for second side")
	}
}

type closeWriteFailingConn struct {
	*testManagedConn
	closeWriteCalled bool
}

func (c *closeWriteFailingConn) CloseWrite() error {
	c.closeWriteCalled = true
	return errors.ErrUnsupported
}

func TestRunManagedCloseWriteFailureFallsBackToClose(t *testing.T) {
	conn := &closeWriteFailingConn{
		testManagedConn: newTestManagedConn(nil),
	}

	err := RunManaged(RunManagedOptions{
		Parent:        context.Background(),
		Conn:          conn,
		TransportSide: SideProvider,
		JoinTimeout:   2 * time.Second,
		Handler: func(ctx context.Context) error {
			return nil
		},
	})
	if err != nil {
		t.Fatalf("RunManaged() = %v, want nil", err)
	}
	if !conn.closeWriteCalled {
		t.Fatal("CloseWrite was not called")
	}
	select {
	case <-conn.closed:
	default:
		t.Fatal("Close was not called after CloseWrite failed")
	}
}

func TestResolveManagedErrors_SuccessAndCancellation(t *testing.T) {
	errTeardown := errors.New("wait: remote command exited without exit status or exit signal")
	errCanceled := context.Canceled

	tests := []struct {
		name    string
		outcome managedOutcome
		wantErr error
	}{
		{
			name: "parent cancelled first wins",
			outcome: managedOutcome{
				firstSide:          SideParent,
				parentErr:          errCanceled,
				handlerErr:         errCanceled,
				transportErr:       errTeardown,
				handlerCompleted:   true,
				transportCompleted: true,
			},
			wantErr: errCanceled,
		},
		{
			name: "handler success beats transport teardown when transport was first",
			outcome: managedOutcome{
				firstSide:          SideProvider,
				handlerErr:         nil,
				transportErr:       errTeardown,
				handlerCompleted:   true,
				transportCompleted: true,
			},
			wantErr: nil,
		},
		{
			name: "handler success beats transport teardown when handler was first",
			outcome: managedOutcome{
				firstSide:          SideSSH,
				handlerErr:         nil,
				transportErr:       errTeardown,
				handlerCompleted:   true,
				transportCompleted: true,
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveManagedErrors(tt.outcome)
			if !errors.Is(got, tt.wantErr) {
				t.Fatalf("resolveManagedErrors() = %v, want %v", got, tt.wantErr)
			}
		})
	}
}

func TestResolveManagedErrors_HandlerError(t *testing.T) {
	errTeardown := errors.New("wait: remote command exited without exit status or exit signal")
	errUser := errors.New("command exited with status 127")

	tests := []struct {
		name    string
		outcome managedOutcome
		wantErr error
	}{
		{
			name: "handler error beats transport teardown when handler was first",
			outcome: managedOutcome{
				firstSide:          SideSSH,
				handlerErr:         errUser,
				transportErr:       errTeardown,
				handlerCompleted:   true,
				transportCompleted: true,
			},
			wantErr: errUser,
		},
		{
			name: "handler error beats transport teardown when transport was first",
			outcome: managedOutcome{
				firstSide:          SideProvider,
				handlerErr:         errUser,
				transportErr:       errTeardown,
				handlerCompleted:   true,
				transportCompleted: true,
			},
			wantErr: errUser,
		},
		{
			name: "transport error returned when handler did not complete in SideSSH",
			outcome: managedOutcome{
				firstSide:          SideSSH,
				transportErr:       errTeardown,
				handlerCompleted:   false,
				transportCompleted: true,
			},
			wantErr: errTeardown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveManagedErrors(tt.outcome)
			if !errors.Is(got, tt.wantErr) {
				t.Fatalf("resolveManagedErrors() = %v, want %v", got, tt.wantErr)
			}
		})
	}
}

func TestResolveManagedErrors_ProviderFailure(t *testing.T) {
	errProvider := errors.New("provider reset")
	errCanceled := context.Canceled

	tests := []struct {
		name    string
		outcome managedOutcome
		wantErr error
	}{
		{
			name: "genuine provider failure wins over handler cancellation consequence",
			outcome: managedOutcome{
				firstSide:          SideProvider,
				handlerErr:         errCanceled,
				transportErr:       errProvider,
				handlerCompleted:   true,
				transportCompleted: true,
			},
			wantErr: errProvider,
		},
		{
			name: "genuine provider failure wins when handler timed out",
			outcome: managedOutcome{
				firstSide:          SideProvider,
				transportErr:       errProvider,
				handlerCompleted:   false,
				transportCompleted: true,
			},
			wantErr: errProvider,
		},
		{
			name: "genuine provider failure wins over later handler success",
			outcome: managedOutcome{
				firstSide:          SideProvider,
				handlerErr:         nil,
				transportErr:       errProvider,
				handlerCompleted:   true,
				transportCompleted: true,
			},
			wantErr: errProvider,
		},
		{
			name: "clean provider exit with handler EOF returns nil",
			outcome: managedOutcome{
				firstSide:          SideProvider,
				handlerErr:         fmt.Errorf("read: %w", io.EOF),
				handlerCompleted:   true,
				transportCompleted: true,
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveManagedErrors(tt.outcome)
			if !errors.Is(got, tt.wantErr) {
				t.Fatalf("resolveManagedErrors() = %v, want %v", got, tt.wantErr)
			}
		})
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
