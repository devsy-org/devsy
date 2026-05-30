package opener

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/tunnel"
)

// containsAdjacent returns true if args contains needle followed immediately
// by value.
func containsAdjacent(args []string, needle, value string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == needle && args[i+1] == value {
			return true
		}
	}
	return false
}

func containsArg(args []string, want string) bool {
	return slices.Contains(args, want)
}

func TestBuildHelperArgs_Basic(t *testing.T) {
	args := buildHelperArgs("ctx-test", "ws-test", tunnel.BrowserTunnelParams{
		TargetURL:        "http://localhost:10800/?folder=/workspace",
		AuthSockID:       "sock-abc",
		User:             "test-user",
		GitSSHSigningKey: "",
	}, false)

	if len(args) < 3 || args[0] != "internal" || args[1] != "helper" ||
		args[2] != "browser-tunnel" {
		t.Fatalf("expected args to start with [internal helper browser-tunnel], got %v", args)
	}

	checkPairs := []struct {
		flag, value string
	}{
		{"--context", "ctx-test"},
		{"--workspace", "ws-test"},
		{"--target-url", "http://localhost:10800/?folder=/workspace"},
		{"--auth-sock-id", "sock-abc"},
		{"--user", "test-user"},
	}
	for _, p := range checkPairs {
		if !containsAdjacent(args, p.flag, p.value) {
			t.Errorf("missing %s %s in %v", p.flag, p.value, args)
		}
	}

	for _, unwanted := range []string{"--forward-ports", "--extra-ports", "--open-browser"} {
		if containsArg(args, unwanted) {
			t.Errorf("unexpected %s for default params: %v", unwanted, args)
		}
	}
}

func TestBuildHelperArgs_ForwardPorts(t *testing.T) {
	args := buildHelperArgs("ctx", "ws", tunnel.BrowserTunnelParams{
		TargetURL:    "http://localhost:1234",
		ForwardPorts: true,
	}, false)
	if !containsArg(args, "--forward-ports") {
		t.Errorf("expected --forward-ports in %v", args)
	}
}

func TestBuildHelperArgs_ExtraPorts(t *testing.T) {
	args := buildHelperArgs("ctx", "ws", tunnel.BrowserTunnelParams{
		TargetURL:  "http://localhost:1234",
		ExtraPorts: []string{"localhost:10800", "127.0.0.1:8443"},
	}, false)
	if !containsAdjacent(args, "--extra-ports", "localhost:10800") {
		t.Errorf("missing --extra-ports localhost:10800 in %v", args)
	}
	if !containsAdjacent(args, "--extra-ports", "127.0.0.1:8443") {
		t.Errorf("missing --extra-ports 127.0.0.1:8443 in %v", args)
	}
}

// TestBuildHelperArgs_OpenBrowser asserts the --open-browser flag is
// emitted iff openBrowser=true.
func TestBuildHelperArgs_OpenBrowser(t *testing.T) {
	withFlag := buildHelperArgs("ctx", "ws", tunnel.BrowserTunnelParams{
		TargetURL: "http://localhost:1234",
	}, true)
	if !containsArg(withFlag, "--open-browser") {
		t.Errorf("expected --open-browser in %v", withFlag)
	}

	withoutFlag := buildHelperArgs("ctx", "ws", tunnel.BrowserTunnelParams{
		TargetURL: "http://localhost:1234",
	}, false)
	if containsArg(withoutFlag, "--open-browser") {
		t.Errorf("did not expect --open-browser in %v", withoutFlag)
	}
}

// setupTempHome redirects the path manager to a temp HOME so the workspace
// dir is writable and isolated from the real user's devsy data.
func setupTempHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	config.ResetPathManager()
	t.Cleanup(config.ResetPathManager)
}

func TestWriteReadTunnelState_RoundTrip(t *testing.T) {
	setupTempHome(t)

	want := TunnelState{
		PID:        12345,
		CreateTime: 67890,
		TargetURL:  "http://localhost:10800",
		Label:      LabelVSCodeBrowser,
	}
	if err := WriteTunnelState("ctx-test", "ws-test", want); err != nil {
		t.Fatalf("WriteTunnelState: %v", err)
	}

	got, err := ReadTunnelState("ctx-test", "ws-test")
	if err != nil {
		t.Fatalf("ReadTunnelState: %v", err)
	}
	if got == nil {
		t.Fatal("ReadTunnelState returned nil after WriteTunnelState")
	}
	if *got != want {
		t.Errorf("round-trip mismatch:\n got=%+v\nwant=%+v", *got, want)
	}

	assertStatePathSane(t, "ctx-test", "ws-test")
}

func assertStatePathSane(t *testing.T, contextName, workspaceID string) {
	t.Helper()
	statePath, err := TunnelStateFilePath(contextName, workspaceID)
	if err != nil {
		t.Fatalf("TunnelStateFilePath: %v", err)
	}
	if !strings.HasPrefix(statePath, os.Getenv("HOME")) {
		t.Errorf("statePath %q is not under HOME %q", statePath, os.Getenv("HOME"))
	}
	if filepath.Base(statePath) != TunnelStateFileName {
		t.Errorf("statePath basename = %q, want %q", filepath.Base(statePath), TunnelStateFileName)
	}
}

func TestHostPortFromURL(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"http with port", "http://localhost:10800/x", "localhost:10800", false},
		{"https with port", "https://example.com:8443/", "example.com:8443", false},
		{"http no port", "http://example.com/", "example.com:80", false},
		{"https no port", "https://example.com/", "example.com:443", false},
		{"ipv6 with port", "http://[::1]:10800/", "[::1]:10800", false},
		{"ipv6 no port", "http://[::1]/", "[::1]:80", false},
		{"ipv6 https no port", "https://[2001:db8::1]/", "[2001:db8::1]:443", false},
		{"empty", "", "", true},
		{"no host", "http:///foo", "", true},
		{"unsupported scheme no port", "ftp://example.com/", "", true},
		{"garbage", "://bad", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := hostPortFromURL(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("hostPortFromURL(%q) = %q, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("hostPortFromURL(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("hostPortFromURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestProbeTCPReachable_Reachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	start := time.Now()
	if err := probeTCPReachable(
		context.Background(),
		ln.Addr().String(),
		2*time.Second,
	); err != nil {
		t.Fatalf("probeTCPReachable(%s) err = %v; want nil", ln.Addr(), err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("probe took %s; expected near-instant success", elapsed)
	}
}

func TestProbeTCPReachable_Unreachable(t *testing.T) {
	// Allocate a port, then close the listener so nothing's listening.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	budget := 600 * time.Millisecond
	start := time.Now()
	err = probeTCPReachable(context.Background(), addr, budget)
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("probeTCPReachable(%s) err = %v; want DeadlineExceeded", addr, err)
	}
	// Must respect the budget (allow some slack for slow CI).
	if elapsed > budget+2*time.Second {
		t.Errorf("probe took %s; budget was %s", elapsed, budget)
	}
}

// TestProbeTCPReachable_CtxCancelledBeforeBudget verifies the probe observes
// ctx cancellation and returns context.Canceled so callers can suppress the
// "never came up" warning.
func TestProbeTCPReachable_CtxCancelledBeforeBudget(t *testing.T) {
	// Allocate and close a port so dials fail immediately.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel from another goroutine shortly after the probe starts so the
	// poll loop observes ctx.Done() rather than the budget.
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	budget := 30 * time.Second
	start := time.Now()
	err = probeTCPReachable(ctx, addr, budget)
	elapsed := time.Since(start)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("probeTCPReachable err = %v; want context.Canceled", err)
	}
	// Must return well before the budget elapses.
	if elapsed > 5*time.Second {
		t.Errorf("probe took %s; expected to return promptly after ctx cancel", elapsed)
	}
}

// TestOpenBrowserWhenReachable_CtxCancelledSilently verifies that when ctx
// is cancelled before the budget expires no warning is emitted (caller
// already gave up, no need to duplicate the error).
func TestOpenBrowserWhenReachable_CtxCancelledSilently(t *testing.T) {
	// Pick a port nothing is listening on.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	done := make(chan struct{})
	start := time.Now()
	go func() {
		openBrowserWhenReachable(ctx, "http://"+addr)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("openBrowserWhenReachable did not return after ctx cancel")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf(
			"openBrowserWhenReachable returned after %s; expected prompt return on ctx cancel",
			elapsed,
		)
	}
}

// TestProbeTCPReachable_BudgetExceedsFiveSeconds proves the probe can wait
// past the legacy 5s budget. Brings the listener up 6s into the probe and
// asserts the probe succeeds rather than timing out.
func TestProbeTCPReachable_BudgetExceedsFiveSeconds(t *testing.T) {
	if testing.Short() {
		t.Skip("test waits >6s")
	}

	addr := allocateClosedAddr(t)
	listenErr := relistenAfter(addr, 6*time.Second, 2*time.Second)

	// Use a budget well above 5s but generous enough to handle CI slowness.
	probeErr := probeTCPReachable(context.Background(), addr, 15*time.Second)

	checkRelistenOrSkip(t, addr, listenErr)
	if probeErr != nil {
		t.Fatalf(
			"probeTCPReachable should succeed once listener comes up at T+6s, got: %v",
			probeErr,
		)
	}
}

// allocateClosedAddr returns a 127.0.0.1 address that was momentarily bound
// then released, so the OS knows the port number but nothing is currently
// listening on it. Used by probe-budget tests to set up a "listener arrives
// later" scenario.
func allocateClosedAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return addr
}

// relistenAfter spawns a goroutine that, after delay, binds addr and holds
// the listener open for hold before closing. The returned channel surfaces
// any net.Listen error (e.g. EADDRINUSE) so the caller can convert a port-
// reuse race into a Skip rather than a hang.
func relistenAfter(addr string, delay, hold time.Duration) <-chan error {
	listenErr := make(chan error, 1)
	go func() {
		time.Sleep(delay)
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			listenErr <- err
			return
		}
		listenErr <- nil
		time.Sleep(hold)
		_ = ln.Close()
	}()
	return listenErr
}

// checkRelistenOrSkip drains a non-blocking read from listenErr. A non-nil
// error means the OS reassigned the port in the close→relisten window;
// skip rather than fail to avoid spurious CI failures on busy hosts.
func checkRelistenOrSkip(t *testing.T, addr string, listenErr <-chan error) {
	t.Helper()
	select {
	case lErr := <-listenErr:
		if lErr != nil {
			t.Skipf("port %s was reassigned between close and relisten: %v", addr, lErr)
		}
	default:
	}
}

func TestReadTunnelState_MissingReturnsNilNil(t *testing.T) {
	setupTempHome(t)

	state := TunnelState{PID: 42, CreateTime: 1, TargetURL: "http://x", Label: LabelVSCodeBrowser}
	if err := WriteTunnelState("ctx-test", "ws-test", state); err != nil {
		t.Fatalf("WriteTunnelState: %v", err)
	}
	statePath, err := TunnelStateFilePath("ctx-test", "ws-test")
	if err != nil {
		t.Fatalf("TunnelStateFilePath: %v", err)
	}
	if err := os.Remove(statePath); err != nil {
		t.Fatalf("remove state file: %v", err)
	}

	got, err := ReadTunnelState("ctx-test", "ws-test")
	if err != nil {
		t.Fatalf("ReadTunnelState after remove: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil state after remove, got %+v", *got)
	}
}
