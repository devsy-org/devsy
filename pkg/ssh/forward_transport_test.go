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

// startTestSSHServer stands up a minimal SSH server on loopback and returns a
// connected client plus a kill func that severs the transport (simulating the
// #759 keep-alive teardown / network drop). Channels are rejected; the client
// only needs a live transport we can later close.
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
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
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

// TestPortForwarding_ReleasesListenerOnTransportDeath is the regression test for
// devsy issue #759's secondary bug. When the SSH transport dies, portForwarding
// must release the local listener (returning ErrTransportClosed) so a reconnect
// can rebind the same host port instead of failing with
// "bind: address already in use".
func TestPortForwarding_ReleasesListenerOnTransportDeath(t *testing.T) {
	client, kill := startTestSSHServer(t)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := lis.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		// exitAfterTimeout=0 disables the idle timer, so only transport death
		// or ctx cancellation can close the listener.
		done <- portForwarding(ctx, client, lis, addr, "tcp", "127.0.0.1:9", 0, forward)
	}()

	// Let the accept loop start, then sever the transport like a network drop.
	time.Sleep(100 * time.Millisecond)
	kill()

	// portForwarding must return promptly with ErrTransportClosed, having
	// released its listener.
	select {
	case err := <-done:
		if !errors.Is(err, ErrTransportClosed) {
			t.Fatalf("expected ErrTransportClosed, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("portForwarding did not return after transport death (listener leaked)")
	}

	// The host port must be rebindable now — this is the reconnect path that
	// previously failed with "bind: address already in use".
	l2, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("rebind on %s failed after transport death: %v", addr, err)
	}
	_ = l2.Close()
}
