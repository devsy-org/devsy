package workspace

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

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
