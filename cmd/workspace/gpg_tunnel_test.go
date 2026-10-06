package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/gpg"
	"github.com/devsy-org/devsy/pkg/port"
	"golang.org/x/crypto/ssh"
)

func TestGPGTunnelEnsureForwardBoundRebindsExitedForward(t *testing.T) {
	firstDone := make(chan error, 1)
	firstDone <- errors.New("forward exited")
	close(firstDone)

	secondDone := make(chan error)
	starts := 0
	tunnel := &gpgTunnel{
		cmd:     &SSHCmd{},
		forward: &managedReverseForward{done: firstDone},
		startForward: func(
			context.Context,
			*ssh.Client,
			[]string,
		) (*managedReverseForward, error) {
			starts++
			return &managedReverseForward{done: secondDone}, nil
		},
	}

	if err := tunnel.ensureForwardBound(
		context.Background(), nil, "/host/gpg-agent.sock",
	); err != nil {
		t.Fatalf("ensureForwardBound() error = %v", err)
	}
	if starts != 1 {
		t.Fatalf("startForward calls = %d, want 1", starts)
	}
	if tunnel.forward == nil || tunnel.forward.done != secondDone {
		t.Fatal("ensureForwardBound() did not retain the replacement forward")
	}
}

func TestGPGTunnelEnsureForwardBoundKeepsActiveForward(t *testing.T) {
	done := make(chan error)
	starts := 0
	tunnel := &gpgTunnel{
		cmd:     &SSHCmd{},
		forward: &managedReverseForward{done: done},
		startForward: func(
			context.Context,
			*ssh.Client,
			[]string,
		) (*managedReverseForward, error) {
			starts++
			return nil, errors.New("unexpected rebind")
		},
	}

	if err := tunnel.ensureForwardBound(
		context.Background(), nil, "/host/gpg-agent.sock",
	); err != nil {
		t.Fatalf("ensureForwardBound() error = %v", err)
	}
	if starts != 0 {
		t.Fatalf("startForward calls = %d, want 0", starts)
	}
}

func TestGPGTunnelRebindsWhenManagedReverseForwardExits(t *testing.T) {
	ctx := context.Background()
	forwards := &testGPGReverseForwards{t: t, userMapping: "127.0.0.1:9000:127.0.0.1:9001"}
	tunnel := &gpgTunnel{
		cmd:          &SSHCmd{ReverseForwardPorts: []string{forwards.userMapping}},
		startForward: forwards.start,
	}
	t.Cleanup(tunnel.stopForward)

	if err := tunnel.ensureForwardBound(ctx, nil, "/host/gpg-agent.sock"); err != nil {
		t.Fatalf("initial ensureForwardBound() error = %v", err)
	}
	if err := forwards.listeners[0].Close(); err != nil {
		t.Fatalf("close first listener: %v", err)
	}
	waitForManagedReverseForward(t, tunnel.forward)
	if err := tunnel.ensureForwardBound(ctx, nil, "/host/gpg-agent.sock"); err != nil {
		t.Fatalf("replacement ensureForwardBound() error = %v", err)
	}
	if len(forwards.listeners) != 2 {
		t.Fatalf("startForward calls = %d, want 2", len(forwards.listeners))
	}
	assertUserReverseForwardActive(t, forwards.userForward, forwards.userListener)
	replacement := tunnel.forward
	tunnel.stopForward()
	waitForManagedReverseForward(t, replacement)
	assertReverseForwardListenerReleased(t, forwards.listeners[1])
}

type testGPGReverseForwards struct {
	t            *testing.T
	userMapping  string
	userForward  *managedReverseForward
	userListener net.Listener
	listeners    []net.Listener
}

func (f *testGPGReverseForwards) start(
	ctx context.Context,
	client *ssh.Client,
	mappings []string,
) (*managedReverseForward, error) {
	want := []string{gpg.ContainerSocketPath + ":/host/gpg-agent.sock"}
	if len(f.listeners) == 0 {
		want = append(want, f.userMapping)
	}
	if !slices.Equal(mappings, want) {
		return nil, fmt.Errorf("forward mappings = %v, want %v", mappings, want)
	}
	if len(f.listeners) == 0 {
		var err error
		f.userForward, f.userListener, err = startTestManagedReverseForward(ctx, client)
		if err != nil {
			return nil, err
		}
		f.t.Cleanup(func() {
			f.userForward.cancel()
			waitForManagedReverseForward(f.t, f.userForward)
		})
	}
	forward, listener, err := startTestManagedReverseForward(ctx, client)
	if err != nil {
		return nil, err
	}
	f.listeners = append(f.listeners, listener)
	return forward, nil
}

func assertUserReverseForwardActive(
	t *testing.T,
	forward *managedReverseForward,
	listener net.Listener,
) {
	t.Helper()
	select {
	case <-forward.done:
		t.Fatal("GPG replacement stopped the independent user forward")
	default:
	}
	duplicate, err := net.Listen("tcp", listener.Addr().String())
	if err == nil {
		_ = duplicate.Close()
		t.Fatal("user forward listener was released during GPG replacement")
	}
}

func assertReverseForwardListenerReleased(t *testing.T, stopped net.Listener) {
	t.Helper()
	if listener, err := net.Listen("tcp", stopped.Addr().String()); err != nil {
		t.Fatalf("replacement listener still bound after stop: %v", err)
	} else {
		_ = listener.Close()
	}
}

func TestGPGTunnelRetriesUserMappingsAfterInitialBindFailure(t *testing.T) {
	userMapping := "127.0.0.1:9000:127.0.0.1:9001"
	want := []string{gpg.ContainerSocketPath + ":/host/gpg-agent.sock", userMapping}
	starts := 0
	tunnel := &gpgTunnel{
		cmd: &SSHCmd{ReverseForwardPorts: []string{userMapping}},
		startForward: func(_ context.Context, _ *ssh.Client, mappings []string) (*managedReverseForward, error) {
			starts++
			if !slices.Equal(mappings, want) {
				t.Fatalf("forward mappings = %v, want %v", mappings, want)
			}
			if starts == 1 {
				return nil, errors.New("initial listener bind failed")
			}
			return &managedReverseForward{done: make(chan error)}, nil
		},
	}
	if err := tunnel.ensureForwardBound(
		context.Background(), nil, "/host/gpg-agent.sock",
	); err == nil {
		t.Fatal("initial listener bind failure was discarded")
	}
	if err := tunnel.ensureForwardBound(
		context.Background(),
		nil,
		"/host/gpg-agent.sock",
	); err != nil {
		t.Fatalf("retry ensureForwardBound() error = %v", err)
	}
	if starts != 2 {
		t.Fatalf("startForward calls = %d, want 2", starts)
	}
}

func startTestManagedReverseForward(
	ctx context.Context,
	client *ssh.Client,
) (*managedReverseForward, net.Listener, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, err
	}
	forwardCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go runManagedReverseForward(managedReverseForwardRun{
		ctx:    forwardCtx,
		cancel: cancel,
		client: client,
		forward: boundReverseForward{
			portMapping: "gpg socket",
			mapping: port.Mapping{
				Host:      port.Address{Protocol: "tcp", Address: listener.Addr().String()},
				Container: port.Address{Protocol: "tcp", Address: "127.0.0.1:1"},
			},
			listener: listener,
		},
		doneChan: done,
	})
	return &managedReverseForward{cancel: cancel, done: done}, listener, nil
}

func waitForManagedReverseForward(t *testing.T, forward *managedReverseForward) {
	t.Helper()
	select {
	case <-forward.done:
	case <-time.After(time.Second):
		t.Fatal("managed forward did not report listener exit")
	}
	select {
	case _, ok := <-forward.done:
		if ok {
			t.Fatal("managed forward reported more than one result")
		}
	case <-time.After(time.Second):
		t.Fatal("managed forward did not close its completion channel")
	}
}

func TestGPGForwardFailureReasonUsesSafeCategory(t *testing.T) {
	err := errors.New("start gpg-agent reverse forward: /private/path")
	if got, want := gpgForwardFailureReason(err), "GPG reverse forwarding failed"; got != want {
		t.Fatalf("gpgForwardFailureReason() = %q, want %q", got, want)
	}
}

func TestWriteGPGForwardDiagnosticRedactsAndScopesDetails(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(
		home, ".devsy", "desktop", "logs", "workspaces", "default", "ws-1", "ssh.log",
	)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create diagnostic directory: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv(gpgForwardDiagnosticFileEnv, path)
	t.Setenv(gpgForwardSessionIDEnv, "session-123")
	t.Setenv("TOKEN", "diagnostic-secret")

	if !writeGPGForwardDiagnostic(errors.New(
		"start gpg-agent reverse forward: token=diagnostic-secret",
	)) {
		t.Fatal("writeGPGForwardDiagnostic() = false, want true")
	}

	// #nosec G304 -- Test path is under its temporary home directory.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read diagnostic: %v", err)
	}
	var record gpgForwardDiagnostic
	if err := json.Unmarshal(bytes.TrimSpace(data), &record); err != nil {
		t.Fatalf("decode diagnostic: %v", err)
	}
	assertGPGForwardDiagnostic(t, record)
	assertPrivateGPGDiagnosticFile(t, path)
}

func TestDesktopGPGDiagnosticPathRejectsOutsideLogRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, "outside.log")
	if _, err := desktopGPGDiagnosticPath(path); err == nil {
		t.Fatalf("desktopGPGDiagnosticPath(%q) returned no error", path)
	}
}

func assertGPGForwardDiagnostic(t *testing.T, record gpgForwardDiagnostic) {
	t.Helper()
	if record.Component != "gpg-forwarding" || record.Code != "reverse_forward_failed" {
		t.Fatalf("diagnostic component/code = %q/%q", record.Component, record.Code)
	}
	if record.SessionID != "session-123" {
		t.Fatalf("diagnostic session id = %q, want session-123", record.SessionID)
	}
	if strings.Contains(record.Message, "diagnostic-secret") {
		t.Fatalf("diagnostic message exposed the secret: %q", record.Message)
	}
	if !strings.Contains(record.Message, "***") {
		t.Fatalf("diagnostic message was not redacted: %q", record.Message)
	}
	if record.Timestamp == "" {
		t.Fatal("diagnostic timestamp is empty")
	}
}

func assertPrivateGPGDiagnosticFile(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat diagnostic: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("diagnostic permissions = %o, want 600", info.Mode().Perm())
	}
}

func TestWriteGPGForwardFailedOSC_WellFormedSequence(t *testing.T) {
	var buf bytes.Buffer
	writeGPGForwardFailedOSC(&buf, "socket did not appear")

	want := fmt.Sprintf("\x1b]%d;socket did not appear\a", gpgForwardFailedOSC)
	if got := buf.String(); got != want {
		t.Fatalf("writeGPGForwardFailedOSC() = %q, want %q", got, want)
	}
}

func TestWriteGPGForwardFailedOSC_StripsControlCharsFromReason(t *testing.T) {
	var buf bytes.Buffer
	writeGPGForwardFailedOSC(&buf, "line one\nline\ttwo\x1b[31mred\a")

	got := buf.String()
	prefix := fmt.Sprintf("\x1b]%d;", gpgForwardFailedOSC)
	if len(got) < len(prefix) || got[:len(prefix)] != prefix {
		t.Fatalf("missing OSC prefix: got %q", got)
	}
	if got[len(got)-1] != '\a' {
		t.Fatalf("missing BEL terminator: got %q", got)
	}
	body := got[len(prefix) : len(got)-1]
	for _, r := range body {
		if r < 0x20 || r == 0x7f {
			t.Fatalf("body still contains control char %q: %q", r, got)
		}
	}
}

func TestWriteGPGForwardFailedOSC_StripsC1ControlsAndSeparator(t *testing.T) {
	var buf bytes.Buffer
	//  is the 8-bit string terminator; ';' is the OSC field separator.
	reason := "abc" + string(rune(0x9c)) + "def;ghi"
	writeGPGForwardFailedOSC(&buf, reason)

	got := buf.String()
	prefix := fmt.Sprintf("\x1b]%d;", gpgForwardFailedOSC)
	body := got[len(prefix) : len(got)-1]
	if body != "abcdefghi" {
		t.Fatalf("body = %q, want %q", body, "abcdefghi")
	}
}

func TestWriteGPGForwardFailedOSC_TruncatesLongReason(t *testing.T) {
	var buf bytes.Buffer
	reason := strings.Repeat("a", gpgForwardFailedReasonMaxLen+100)
	writeGPGForwardFailedOSC(&buf, reason)

	got := buf.String()
	prefix := fmt.Sprintf("\x1b]%d;", gpgForwardFailedOSC)
	body := got[len(prefix) : len(got)-1]
	if len(body) != gpgForwardFailedReasonMaxLen {
		t.Fatalf("body length = %d, want %d", len(body), gpgForwardFailedReasonMaxLen)
	}
}

func TestWriteGPGForwardFailedOSC_TruncatesByRunesNotBytes(t *testing.T) {
	var buf bytes.Buffer
	// "é" is 2 bytes in UTF-8; byte-based truncation would produce a body
	// longer than gpgForwardFailedReasonMaxLen bytes or split a rune in two.
	reason := strings.Repeat("é", gpgForwardFailedReasonMaxLen+100)
	writeGPGForwardFailedOSC(&buf, reason)

	got := buf.String()
	prefix := fmt.Sprintf("\x1b]%d;", gpgForwardFailedOSC)
	body := got[len(prefix) : len(got)-1]
	if n := len([]rune(body)); n != gpgForwardFailedReasonMaxLen {
		t.Fatalf("body rune count = %d, want %d", n, gpgForwardFailedReasonMaxLen)
	}
}
