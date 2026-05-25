package ssh

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// noopForward is a ForwardingFunction that immediately closes the local
// connection. This lets portForwarding exercise its idle/cancellation paths
// without needing a real *ssh.Client.
func noopForward(localConn net.Conn, _ *ssh.Client, _, _ string) {
	_ = localConn.Close()
}

// TestPortForwarding_IdleTimeoutReturnsErrIdleTimeout verifies that when the
// idle timeout fires after a connection has come and gone, portForwarding
// returns ErrIdleTimeout.
func TestPortForwarding_IdleTimeoutReturnsErrIdleTimeout(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- portForwarding(
			ctx, nil, lis,
			lis.Addr().String(), "tcp", "127.0.0.1:0",
			15*time.Millisecond, noopForward,
		)
	}()

	// Open and close a connection so the connection counter starts the idle
	// timer when it returns to zero.
	conn, err := net.Dial("tcp", lis.Addr().String())
	if err != nil {
		t.Fatalf("failed to dial listener: %v", err)
	}
	_ = conn.Close()

	select {
	case got := <-errCh:
		if !errors.Is(got, ErrIdleTimeout) {
			t.Fatalf("expected ErrIdleTimeout, got %v", got)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for portForwarding to return")
	}
}

// TestPortForwarding_ParentCancelNotIdleTimeout verifies that when the parent
// context is canceled, the returned error is NOT ErrIdleTimeout.
func TestPortForwarding_ParentCancelNotIdleTimeout(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- portForwarding(
			ctx, nil, lis,
			lis.Addr().String(), "tcp", "127.0.0.1:0",
			// Use a long idle timeout so it cannot fire during the test.
			10*time.Second, noopForward,
		)
	}()

	// Cancel before any idle timer could be relevant.
	cancel()

	select {
	case got := <-errCh:
		if errors.Is(got, ErrIdleTimeout) {
			t.Fatalf("expected non-ErrIdleTimeout error on parent cancel, got %v", got)
		}
		if got == nil {
			t.Fatal("expected a non-nil error on parent cancel")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for portForwarding to return")
	}
}
