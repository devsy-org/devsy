package log

import (
	"bytes"
	"strings"
	"testing"
)

func TestWriter_EmitsStructuredJSONLine(t *testing.T) {
	Init(Config{Verbosity: 2, Format: "json"})

	var sink bytes.Buffer
	remove := AddSink(&sink)
	defer remove()

	w := Writer(LevelInfo)
	_, err := w.Write([]byte("Cloning into 'repo'...\n"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	_ = Sync()

	got := strings.TrimSpace(sink.String())
	if !strings.HasPrefix(got, "{") || !strings.HasSuffix(got, "}") {
		t.Fatalf("expected a single JSON object, got %q", got)
	}
	if !strings.Contains(got, `"msg":"Cloning into 'repo'..."`) {
		t.Errorf("missing expected msg field: %q", got)
	}
	if !strings.Contains(got, `"level":"info"`) {
		t.Errorf("missing expected level field: %q", got)
	}
}

func TestWriter_SplitsMultipleLinesInOneWrite(t *testing.T) {
	Init(Config{Verbosity: 2, Format: "json"})

	var sink bytes.Buffer
	remove := AddSink(&sink)
	defer remove()

	w := Writer(LevelInfo)
	_, err := w.Write([]byte("line one\nline two\n"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	_ = Sync()

	lines := strings.Split(strings.TrimSpace(sink.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %q", len(lines), lines)
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "{") || !strings.HasSuffix(l, "}") {
			t.Errorf("line is not valid single-object JSON: %q", l)
		}
	}
}

func TestWriter_FlushesTrailingPartialLineOnClose(t *testing.T) {
	Init(Config{Verbosity: 2, Format: "json"})

	var sink bytes.Buffer
	remove := AddSink(&sink)
	defer remove()

	w := Writer(LevelInfo)
	_, _ = w.Write([]byte("no trailing newline"))
	_ = Sync()
	if sink.Len() != 0 {
		t.Errorf("expected nothing logged before Close, got %q", sink.String())
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	_ = Sync()

	if !strings.Contains(sink.String(), "no trailing newline") {
		t.Errorf("expected trailing partial line flushed on Close, got %q", sink.String())
	}
}

func TestWriter_DiscardsBelowConfiguredLevel(t *testing.T) {
	Init(Config{Verbosity: 1, Format: "json"}) // info+ only, debug disabled

	var sink bytes.Buffer
	remove := AddSink(&sink)
	defer remove()

	w := Writer(LevelDebug)
	_, _ = w.Write([]byte("should not appear\n"))
	_ = w.Close()
	_ = Sync()

	if sink.Len() != 0 {
		t.Errorf("expected debug output discarded below configured level, got %q", sink.String())
	}
}
