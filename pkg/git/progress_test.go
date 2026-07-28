package git

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestProgressWriter_SplitsOnCarriageReturn(t *testing.T) {
	var out bytes.Buffer
	w := newProgressWriter(&out)

	_, err := w.Write([]byte(
		"Cloning into 'repo'...\n" +
			"Receiving objects:   0% (1/333)\r" +
			"Receiving objects:  10% (34/333)\r" +
			"Receiving objects: 100% (333/333), done.\n",
	))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Only the plain informational line reaches the log; percentage frames
	// are logged separately (via pkg/log, not through `out`).
	if got := strings.TrimRight(out.String(), "\n"); got != "Cloning into 'repo'..." {
		t.Errorf("log output = %q", got)
	}
}

func TestProgressWriter_ThinsIntermediatePercentages(t *testing.T) {
	var out bytes.Buffer
	w := newProgressWriter(&out)

	for pct := 0; pct <= 100; pct++ {
		_, _ = w.Write(fmt.Appendf(nil, "Receiving objects: %3d%% (1/1)\r", pct))
	}
	_ = w.Close()

	// Percentage frames never reach `next`, regardless of thinning.
	if out.Len() != 0 {
		t.Errorf("expected no log output, got %q", out.String())
	}
}

func TestProgressWriter_DropsExactDuplicateFrame(t *testing.T) {
	var out bytes.Buffer
	w := newProgressWriter(&out)

	_, _ = w.Write([]byte(
		"Receiving objects:  10% (34/333)\r" +
			"Receiving objects:  10% (34/333)\r",
	))
	_ = w.Close()

	if out.Len() != 0 {
		t.Errorf("expected no log output, got %q", out.String())
	}
}

func TestProgressWriter_TracksLabelsIndependently(t *testing.T) {
	var out bytes.Buffer
	w := newProgressWriter(&out)

	_, _ = w.Write([]byte(
		"Receiving objects:  10% (34/333)\r" +
			"Resolving deltas:  10% (10/100)\r",
	))
	_ = w.Close()

	if out.Len() != 0 {
		t.Errorf("expected no log output, got %q", out.String())
	}
}

func TestProgressWriter_PassesThroughNonProgressLines(t *testing.T) {
	var out bytes.Buffer
	w := newProgressWriter(&out)

	_, _ = w.Write([]byte(
		"Cloning into 'repo'...\n" +
			"remote: Enumerating objects: 333, done.\n",
	))
	_ = w.Close()

	got := out.String()
	if !strings.Contains(got, "Cloning into 'repo'...") ||
		!strings.Contains(got, "remote: Enumerating objects: 333, done.") {
		t.Errorf("expected both lines passed through unchanged, got %q", got)
	}
}

func TestProgressWriter_FlushesTrailingPartialLineOnClose(t *testing.T) {
	var out bytes.Buffer
	w := newProgressWriter(&out)

	_, _ = w.Write([]byte("no trailing newline"))
	if out.Len() != 0 {
		t.Errorf("expected nothing forwarded before close, got %q", out.String())
	}
	_ = w.Close()
	if got := strings.TrimRight(out.String(), "\n"); got != "no trailing newline" {
		t.Errorf("got %q", got)
	}
}
