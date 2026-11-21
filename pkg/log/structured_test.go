package log

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/logging"
	"github.com/devsy-org/devsy/logging/redact"
)

const testCanary = "canary"

func TestStructuredLoggingRedactsResolvedNestedValues(t *testing.T) {
	const secret = "private\"token\nvalue"
	Init(
		Config{
			Level:    LevelTraceName,
			Format:   testFormatJSON,
			Redactor: redact.FromEntries([]string{"TOKEN=" + secret}),
		},
	)
	var output syncBuffer
	remove := AddSink(&output)
	defer remove()
	ctx := WithContext(context.Background(), slog.String("component", testCanary))
	InfoContext(
		ctx,
		"hello "+secret,
		slog.Any("nested", map[string]any{"value": []any{secret, "visible"}}),
	)
	if err := Sync(); err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(output.String()), &record); err != nil {
		t.Fatal(err)
	}
	if record["component"] != testCanary || !strings.Contains(output.String(), "visible") ||
		strings.Contains(output.String(), "private") {
		t.Fatalf("unexpected record %q", output.String())
	}
}

func TestStreamerPreservesStructuredDataAndRedactsCapture(t *testing.T) {
	logs := InitTestObserved(t, logging.LevelTrace)
	secret := "private\"token"
	stream := NewJSONLogStreamer(StreamerOptions{
		FallbackLevel: slog.LevelInfo,
		CaptureLines:  1,
		CaptureBytes:  1024,
		Redactor:      redact.FromEntries([]string{"TOKEN=" + secret}),
		DefaultAttrs: []slog.Attr{
			slog.String("provider", "default"),
			slog.String("component", testCanary),
		},
	})
	line := `{"level":"error","ts":"2026-01-02T03:04:05.123Z","msg":"failure",` +
		`"provider":"explicit","error_code":"unavailable","nested":{"number":9007199254740993,"secret":"private\"token"}}`
	if _, err := stream.Write([]byte(line + "\n")); err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	entries := logs.All()
	assertStructuredStream(t, entries, stream)
}

func assertStructuredStream(t *testing.T, entries []LoggedEntry, stream *JSONLogStreamer) {
	t.Helper()
	if len(entries) != 1 || entries[0].Level != slog.LevelError ||
		entries[0].AttrsMap()["provider"] != "explicit" ||
		entries[0].AttrsMap()["component"] != testCanary ||
		stream.ErrorCode() != "unavailable" {
		t.Fatalf("unexpected record: %#v code=%q", entries, stream.ErrorCode())
	}
	if strings.Contains(stream.ErrorOutput(), "private") ||
		!strings.Contains(stream.ErrorOutput(), "9007199254740993") {
		t.Fatalf("unexpected captured output %q", stream.ErrorOutput())
	}
}

func TestStreamerCleansRootDefaultsAndKeepsNestedFields(t *testing.T) {
	logs := InitTestObserved(t, logging.LevelTrace)
	stream := NewJSONLogStreamer(StreamerOptions{
		FallbackLevel: slog.LevelInfo,
		DefaultAttrs: []slog.Attr{
			slog.String("level", "forged"),
			slog.String("provider", "default"),
			slog.String("component", testCanary),
			slog.Group("", slog.String(slog.MessageKey, "forged")),
			slog.Group("nested", slog.String("level", "nested")),
		},
	})
	if _, err := stream.Write(
		[]byte(`{"level":"info","msg":"actual","provider":"explicit"}` + "\n"),
	); err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("unexpected entries: %#v", entries)
	}
	assertCleanStreamAttrs(t, entries[0])
}

func assertCleanStreamAttrs(t *testing.T, entry LoggedEntry) {
	t.Helper()
	attrs := entry.AttrsMap()
	if entry.Message != "actual" || attrs["provider"] != "explicit" ||
		attrs["component"] != testCanary ||
		attrs["level"] != nil ||
		attrs[slog.MessageKey] != nil {
		t.Fatalf("unexpected entry: %#v attrs=%#v", entry, attrs)
	}
	nested, ok := attrs["nested"].(map[string]any)
	if !ok || nested["level"] != "nested" {
		t.Fatalf("unexpected nested attrs: %#v", attrs)
	}
}

type countedStringer struct{ calls *int }

func (s countedStringer) String() string { *s.calls++; return "formatted" }

func TestDisabledFormattedLogDoesNotFormat(t *testing.T) {
	Init(Config{Level: LevelWarnName})
	calls := 0
	Debugf("%s", countedStringer{calls: &calls})
	if calls != 0 {
		t.Fatalf("disabled message formatted %d times", calls)
	}
}

func TestObservedGroupsAndFiltering(t *testing.T) {
	logs := InitTestObserved(t, logging.LevelTrace)
	logger := Slog().With(slog.String("component", testCanary)).
		WithGroup("outer").With(slog.Int("count", 3)).WithGroup("inner")
	logger.LogAttrs(
		context.Background(),
		logging.LevelTrace,
		"event",
		slog.String("phase", "ready"),
	)
	entries := logs.FilterAttr(slog.String("component", testCanary)).
		FilterLevelExact(logging.LevelTrace).
		All()
	if len(entries) != 1 {
		t.Fatalf("unexpected entries: %#v", entries)
	}
	outer, ok := entries[0].AttrsMap()["outer"].(map[string]any)
	if !ok || outer["count"] != int64(3) {
		t.Fatalf("unexpected groups: %#v", entries[0].AttrsMap())
	}
	inner, ok := outer["inner"].(map[string]any)
	if !ok || inner["phase"] != "ready" {
		t.Fatalf("unexpected inner group: %#v", outer)
	}
}

func TestWriterRedactsSecretAcrossPendingLimit(t *testing.T) {
	secret := "stream-boundary-secret"
	Init(
		Config{
			Level:    LevelInfoName,
			Format:   testFormatJSON,
			Redactor: redact.FromEntries([]string{"TOKEN=" + secret}),
		},
	)
	var output syncBuffer
	remove := AddSink(&output)
	defer remove()
	writer := Writer(slog.LevelInfo)
	if _, err := writer.Write(
		[]byte(strings.Repeat("x", maxPendingLine) + "stream-boundary-"),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("secret\nnext canary\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := Sync(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), secret) ||
		strings.Contains(output.String(), "stream-boundary-") ||
		!strings.Contains(output.String(), "next canary") {
		t.Fatalf("unexpected output: %q", output.String())
	}
}

func TestStreamerPreservesEmittedAndRFC3339Timestamps(t *testing.T) {
	want := time.Date(2026, 1, 2, 3, 4, 5, 123000000, time.FixedZone("offset", -7*60*60))
	Init(Config{Level: LevelTraceName, Format: "json"})
	var output syncBuffer
	remove := AddSink(&output)
	emitRecord(slog.LevelInfo, want, "round trip", nil)
	if err := Sync(); err != nil {
		t.Fatal(err)
	}
	remove()

	logs := InitTestObserved(t, logging.LevelTrace)
	stream := NewJSONLogStreamer(StreamerOptions{FallbackLevel: slog.LevelInfo})
	if _, err := stream.Write([]byte(output.String())); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Write([]byte(`{"level":"info","ts":"2026-01-02T03:04:05.123-07:00",` +
		`"msg":"standard"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	entries := logs.All()
	if len(entries) != 2 {
		t.Fatalf("unexpected entries: %#v", entries)
	}
	for _, entry := range entries {
		if !entry.Time.Equal(want) {
			t.Fatalf("timestamp %v, want %v", entry.Time, want)
		}
	}
}

func TestStructuredLoggingProtectsEnvelope(t *testing.T) {
	Init(Config{Level: LevelTraceName, Format: testFormatJSON})
	var output syncBuffer
	remove := AddSink(&output)
	defer remove()
	ctx := WithContext(
		context.Background(),
		slog.String("component", "context"),
		slog.String("level", "debug"),
	)
	FromContext(
		ctx,
	).With(slog.String(slog.MessageKey, "forged")).
		LogAttrs(ctx, slog.LevelInfo, "actual",
			slog.String("ts", "forged"),
			slog.String(slog.TimeKey, "forged"), slog.String("message", "forged"),
			slog.Group("", slog.String("level", "forged")),
			slog.Group("nested", slog.String("level", "nested")))
	if err := Sync(); err != nil {
		t.Fatal(err)
	}
	line := output.String()
	var record map[string]any
	if err := json.Unmarshal([]byte(line), &record); err != nil {
		t.Fatal(err)
	}
	assertProtectedEnvelope(t, record, line)
}

func assertProtectedEnvelope(t *testing.T, record map[string]any, line string) {
	t.Helper()
	if record["level"] != "info" || record[slog.MessageKey] != "actual" ||
		record["component"] != "context" ||
		record[slog.TimeKey] != nil ||
		record["message"] != nil {
		t.Fatalf("unexpected record %s", line)
	}
	assertEnvelopeCounts(t, line)
	nested, ok := record["nested"].(map[string]any)
	if !ok || nested["level"] != "nested" {
		t.Fatalf("unexpected nested fields %s", line)
	}
}

func assertEnvelopeCounts(t *testing.T, line string) {
	t.Helper()
	if strings.Count(line, `"level":`) != 2 || strings.Count(line, `"msg":`) != 1 ||
		strings.Count(line, `"ts":`) != 1 {
		t.Fatalf("unexpected envelope %s", line)
	}
}
