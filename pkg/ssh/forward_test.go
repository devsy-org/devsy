package ssh

import (
	"context"
	"crypto/ed25519"
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

func startTestSSHServer(t *testing.T) (client *ssh.Client, kill func()) {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}

	srvCfg := &ssh.ServerConfig{NoClientAuth: true}
	srvCfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	serverConnCh := make(chan net.Conn, 1)
	go func() {
		nConn, aerr := ln.Accept()
		if aerr != nil {
			return
		}
		serverConnCh <- nConn
		sConn, chans, reqs, herr := ssh.NewServerConn(nConn, srvCfg)
		if herr != nil {
			return
		}
		go ssh.DiscardRequests(reqs)
		go func() {
			for nc := range chans {
				_ = nc.Reject(ssh.Prohibited, "no channels in test")
			}
		}()
		_ = sConn
	}()

	cConn, err := ssh.Dial("tcp", ln.Addr().String(), &ssh.ClientConfig{
		User:            "test",
		HostKeyCallback: ssh.FixedHostKey(signer.PublicKey()),
		Timeout:         2 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	serverConn := <-serverConnCh
	kill = func() {
		_ = serverConn.Close()
		_ = ln.Close()
	}
	t.Cleanup(func() { _ = cConn.Close(); kill() })
	return cConn, kill
}

func TestPortForwarding_ReleasesListenerOnTransportDeath(t *testing.T) {
	client, kill := startTestSSHServer(t)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := lis.Addr().String()

	done := make(chan error, 1)
	go func() {
		done <- portForwarding(t.Context(), client, lis, addr, "tcp", "127.0.0.1:9", 0, forward)
	}()

	time.Sleep(100 * time.Millisecond)
	kill()

	select {
	case err := <-done:
		if !errors.Is(err, ErrTransportClosed) {
			t.Fatalf("expected ErrTransportClosed, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("portForwarding did not return after transport death (listener leaked)")
	}

	l2, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("rebind on %s failed after transport death: %v", addr, err)
	}
	_ = l2.Close()
}
