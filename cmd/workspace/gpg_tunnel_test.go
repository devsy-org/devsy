package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

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

func TestGPGForwardFailureReasonUsesSafeCategory(t *testing.T) {
	err := errors.New("start gpg-agent reverse forward: /private/path")
	if got, want := gpgForwardFailureReason(err), "GPG reverse forwarding failed"; got != want {
		t.Fatalf("gpgForwardFailureReason() = %q, want %q", got, want)
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
