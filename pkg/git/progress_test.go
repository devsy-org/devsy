package git

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/log"
)

const testLogFormatText = "text"

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
	log.Init(log.Config{Verbosity: 2, Format: testLogFormatText})
	var sink bytes.Buffer
	remove := log.AddSink(&sink)
	defer remove()

	var out bytes.Buffer
	w := newProgressWriter(&out)

	for pct := 0; pct <= 100; pct++ {
		_, _ = w.Write(fmt.Appendf(nil, "Receiving objects: %3d%% (1/1)\r", pct))
	}
	_ = w.Close()
	_ = log.Sync()

	// Percentage frames never reach `next`, regardless of thinning.
	if out.Len() != 0 {
		t.Errorf("expected no log output via next, got %q", out.String())
	}

	// Only 0, 10, 20, ..., 100 should have been logged, in that order.
	lines := strings.Split(strings.TrimSpace(sink.String()), "\n")
	if len(lines) != 11 {
		t.Fatalf("got %d progress log lines, want 11: %q", len(lines), sink.String())
	}
	for i, want := range []int{0, 10, 20, 30, 40, 50, 60, 70, 80, 90, 100} {
		wantSub := fmt.Sprintf("%d%%", want)
		if !strings.Contains(lines[i], wantSub) {
			t.Errorf("line %d = %q, want to contain %q", i, lines[i], wantSub)
		}
	}
}

func TestProgressWriter_DropsExactDuplicateFrame(t *testing.T) {
	log.Init(log.Config{Verbosity: 2, Format: testLogFormatText})
	var sink bytes.Buffer
	remove := log.AddSink(&sink)
	defer remove()

	var out bytes.Buffer
	w := newProgressWriter(&out)

	_, _ = w.Write([]byte(
		"Receiving objects:  10% (34/333)\r" +
			"Receiving objects:  10% (34/333)\r",
	))
	_ = w.Close()
	_ = log.Sync()

	lines := strings.Split(strings.TrimSpace(sink.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf(
			"got %d progress log lines, want 1 (duplicate dropped): %q",
			len(lines), sink.String(),
		)
	}
}

func TestProgressWriter_TracksLabelsIndependently(t *testing.T) {
	log.Init(log.Config{Verbosity: 2, Format: testLogFormatText})
	var sink bytes.Buffer
	remove := log.AddSink(&sink)
	defer remove()

	var out bytes.Buffer
	w := newProgressWriter(&out)

	_, _ = w.Write([]byte(
		"Receiving objects:  10% (34/333)\r" +
			"Resolving deltas:  10% (10/100)\r",
	))
	_ = w.Close()
	_ = log.Sync()

	got := sink.String()
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 2 {
		t.Fatalf(
			"got %d progress log lines, want 2 (independent labels): %q",
			len(lines), got,
		)
	}
	if !strings.Contains(lines[0], "Receiving objects") {
		t.Errorf("line 0 = %q, want to contain %q", lines[0], "Receiving objects")
	}
	if !strings.Contains(lines[1], "Resolving deltas") {
		t.Errorf("line 1 = %q, want to contain %q", lines[1], "Resolving deltas")
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
