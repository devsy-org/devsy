package log

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/secrets"
)

const testFormatJSON = "json"

func TestWriter_EmitsStructuredJSONLine(t *testing.T) {
	Init(Config{Verbosity: 2, Format: testFormatJSON})

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

func TestLoggerFormatsHaveStableRecords(t *testing.T) { //nolint:cyclop // table covers all supported log formats
	for _, format := range []string{"text", "json", "logfmt"} {
		t.Run(format, func(t *testing.T) {
			Init(Config{Verbosity: 1, Format: format})
			var sink syncBuffer
			remove := AddSink(&sink)
			defer remove()
			Infof("format canary")
			_ = Sync()
			line := strings.TrimSpace(sink.String())
			if strings.ContainsAny(line, "\x1b\r") {
				t.Fatalf("format %q contains terminal control characters: %q", format, line)
			}
			switch format {
			case "json":
				var record map[string]any
				if err := json.Unmarshal([]byte(line), &record); err != nil {
					t.Fatalf("JSON log is invalid: %v (%q)", err, line)
				}
			case "logfmt":
				if !strings.Contains(line, "level=info") || !strings.Contains(line, "msg=") {
					t.Fatalf("logfmt fields missing: %q", line)
				}
			default:
				if !strings.Contains(line, "INFO") || !strings.Contains(line, "format canary") {
					t.Fatalf("text fields missing: %q", line)
				}
			}
		})
	}
}

func TestWriter_SplitsMultipleLinesInOneWrite(t *testing.T) {
	Init(Config{Verbosity: 2, Format: testFormatJSON})

	var sink bytes.Buffer
	remove := AddSink(&sink)
	defer remove()

	w := Writer(LevelInfo)
	_, err := w.Write([]byte("line one\nline two\n"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	// A line split across two Write calls (as os/exec delivers subprocess
	// output in arbitrary chunks) must still be logged as one complete
	// record, not two fragments.
	if _, err := w.Write([]byte("line three\nline ")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := w.Write([]byte("four\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	_ = Sync()

	lines := strings.Split(strings.TrimSpace(sink.String()), "\n")
	wantMsgs := []string{"line one", "line two", "line three", "line four"}
	if len(lines) != len(wantMsgs) {
		t.Fatalf("got %d lines, want %d: %q", len(lines), len(wantMsgs), lines)
	}
	for i, l := range lines {
		assertJSONLineMsg(t, i, l, wantMsgs[i])
	}
}

func assertJSONLineMsg(t *testing.T, i int, line, wantMsg string) {
	t.Helper()
	if !strings.HasPrefix(line, "{") || !strings.HasSuffix(line, "}") {
		t.Errorf("line %d is not valid single-object JSON: %q", i, line)
		return
	}
	var rec struct {
		Msg string `json:"msg"`
	}
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		t.Errorf("line %d: json.Unmarshal: %v", i, err)
		return
	}
	if rec.Msg != wantMsg {
		t.Errorf("line %d msg = %q, want %q", i, rec.Msg, wantMsg)
	}
}

func TestWriter_FlushesTrailingPartialLineOnClose(t *testing.T) {
	Init(Config{Verbosity: 2, Format: testFormatJSON})

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

func TestWriter_PreservesBlankLines(t *testing.T) {
	Init(Config{Verbosity: 2, Format: testFormatJSON})

	var sink bytes.Buffer
	remove := AddSink(&sink)
	defer remove()

	w := Writer(LevelInfo)
	if _, err := w.Write([]byte("one\n\ntwo\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	_ = Sync()

	lines := strings.Split(strings.TrimSpace(sink.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3 (blank line preserved): %q", len(lines), lines)
	}
	if !strings.Contains(lines[1], `"msg":""`) {
		t.Errorf("line 1 = %q, want an empty msg field", lines[1])
	}
}

func TestPassthroughWriter_WritesRawBytesUnformatted(t *testing.T) {
	Init(Config{Quiet: true, Format: testFormatJSON})

	var sink bytes.Buffer
	remove := AddSink(&sink)
	defer remove()

	w := PassthroughWriter()
	if _, err := w.Write([]byte("raw output\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if got := sink.String(); got != "raw output\n" {
		t.Errorf("got %q, want raw bytes with no structured formatting", got)
	}
}

func TestPassthroughWriterRedactsSplitSecrets(t *testing.T) {
	Init(Config{Quiet: true, Format: testFormatJSON})

	var sink syncBuffer
	remove := AddSink(&sink)
	defer remove()

	w := PassthroughWriterWithRedactor(secrets.NewRedactor([]string{
		"TOKEN=DEVSY_PASSTHROUGH_SECRET_846301",
	}))
	_, _ = w.Write([]byte("token=DEVSY_PASSTHROUGH_"))
	_, _ = w.Write([]byte("SECRET_846301\n"))
	_ = w.Close()

	if got := sink.String(); strings.Contains(got, "DEVSY_PASSTHROUGH_SECRET_846301") {
		t.Fatalf("passthrough writer leaked secret: %q", got)
	}
	if !strings.Contains(sink.String(), "token=***") {
		t.Fatalf("passthrough writer did not redact secret: %q", sink.String())
	}
}

func TestWriter_DiscardsBelowConfiguredLevel(t *testing.T) {
	Init(Config{Verbosity: 1, Format: testFormatJSON}) // info+ only, debug disabled

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
