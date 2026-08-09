package port

import (
	"net"
	"strconv"
	"testing"
)

func freePort(t *testing.T) (int, *net.TCPListener) {
	t.Helper()
	l, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("listen for free port: %v", err)
	}
	return l.Addr().(*net.TCPAddr).Port, l
}

func TestIsAvailable_FreePort(t *testing.T) {
	port, l := freePort(t)
	if err := l.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}

	addr := "127.0.0.1:" + strconv.Itoa(port)
	got, err := IsAvailable(addr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got {
		t.Errorf("expected port %s to be available", addr)
	}
}

func TestIsAvailable_OccupiedPort(t *testing.T) {
	port, l := freePort(t)
	defer func() { _ = l.Close() }()

	addr := "127.0.0.1:" + strconv.Itoa(port)
	got, err := IsAvailable(addr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Errorf("expected occupied port %s to be unavailable", addr)
	}
}

func TestIsAvailable_InvalidAddr(t *testing.T) {
	got, err := IsAvailable("not-a-valid-address")
	if err == nil {
		t.Fatal("expected error for invalid address")
	}
	if got {
		t.Error("expected invalid address to be unavailable")
	}
}

func TestFindAvailablePort_ReturnsStartWhenFree(t *testing.T) {
	port, l := freePort(t)
	if err := l.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}

	got, err := FindAvailablePort(port)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != port {
		t.Errorf("expected first available port %d, got %d", port, got)
	}
}

func TestFindAvailablePort_SkipsOccupiedPort(t *testing.T) {
	occupied, l := freePort(t)
	defer func() { _ = l.Close() }()

	got, err := FindAvailablePort(occupied)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == occupied {
		t.Fatalf("expected a port other than the occupied %d", occupied)
	}

	available, err := IsAvailable("127.0.0.1:" + strconv.Itoa(got))
	if err != nil {
		t.Fatalf("checking returned port: %v", err)
	}
	if !available {
		t.Errorf("returned port %d should be available", got)
	}
}
