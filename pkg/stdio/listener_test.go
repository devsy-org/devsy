package stdio

import (
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func acceptWithTimeout(t *testing.T, listener net.Listener) (net.Conn, error) {
	t.Helper()
	result := make(chan struct {
		conn net.Conn
		err  error
	}, 1)
	go func() {
		conn, err := listener.Accept()
		result <- struct {
			conn net.Conn
			err  error
		}{conn, err}
	}()
	select {
	case result := <-result:
		return result.conn, result.err
	case <-time.After(2 * time.Second):
		t.Fatal("listener Accept timed out")
		return nil, nil
	}
}

func TestStdioListenerAcceptsOneConnection(t *testing.T) {
	listener := NewStdioListener(strings.NewReader(""), &trackingWriteCloser{})
	conn, err := acceptWithTimeout(t, listener)
	if err != nil {
		t.Fatalf("first Accept() error = %v", err)
	}
	if conn == nil {
		t.Fatal("first Accept() returned nil connection")
	}
	defer func() { _ = listener.Close() }()

	secondDone := make(chan error, 1)
	go func() {
		_, err := listener.Accept()
		secondDone <- err
	}()
	if err := conn.Close(); err != nil {
		t.Fatalf("connection Close() error = %v", err)
	}

	select {
	case err := <-secondDone:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("second Accept() error = %v, want net.ErrClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second Accept() remained blocked after connection close")
	}
}

func TestStdioListenerCloseUnblocksAccept(t *testing.T) {
	listener := NewStdioListener(strings.NewReader(""), &trackingWriteCloser{})
	if _, err := acceptWithTimeout(t, listener); err != nil {
		t.Fatalf("first Accept() error = %v", err)
	}

	secondDone := make(chan error, 1)
	go func() {
		_, err := listener.Accept()
		secondDone <- err
	}()
	if err := listener.Close(); err != nil {
		t.Fatalf("listener Close() error = %v", err)
	}

	select {
	case err := <-secondDone:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("second Accept() error = %v, want net.ErrClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second Accept() remained blocked after listener close")
	}
}

func TestStdioListenerClosedBeforeAccept(t *testing.T) {
	listener := NewStdioListener(strings.NewReader(""), &trackingWriteCloser{})
	if err := listener.Close(); err != nil {
		t.Fatalf("listener Close() error = %v", err)
	}

	if _, err := listener.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Accept() error = %v, want net.ErrClosed", err)
	}
}

func TestStdioListenerRepeatedCloseOrders(t *testing.T) {
	listener := NewStdioListener(strings.NewReader(""), &trackingWriteCloser{})
	conn, err := acceptWithTimeout(t, listener)
	if err != nil {
		t.Fatalf("first Accept() error = %v", err)
	}

	if err := conn.Close(); err != nil {
		t.Fatalf("connection Close() error = %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("listener Close() after connection close error = %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("repeated listener Close() error = %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("repeated connection Close() error = %v", err)
	}
}
