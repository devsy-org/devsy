package log

import (
	"bytes"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/logging/redact"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const streamNextMode = "next"

func TestJSONLogStreamerRedactsCapturedLines(t *testing.T) {
	streamer := NewJSONLogStreamer(StreamerOptions{
		FallbackLevel: slog.LevelInfo,
		CaptureLines:  2,
		Redactor:      redact.FromEntries([]string{"TOKEN=DEVSY_STREAM_SECRET_846297"}),
	})
	_, _ = streamer.Write([]byte("token=DEVSY_STREAM_SECRET_846297\n"))
	if err := streamer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := streamer.ErrorOutput(); strings.Contains(got, "DEVSY_STREAM_SECRET_846297") {
		t.Fatalf("captured output leaked secret: %q", got)
	}
}

func TestJSONLogStreamerCombinesConfiguredAndEnvironmentRedaction(t *testing.T) {
	t.Setenv("DEVSY_STREAM_ENV_SECRET", "stream-env-secret-846299")
	streamer := NewJSONLogStreamer(StreamerOptions{
		FallbackLevel: slog.LevelInfo,
		CaptureLines:  1,
		Redactor:      redact.FromEntries([]string{"TOKEN=stream-config-secret-846300"}),
	})
	_, _ = streamer.Write([]byte("stream-env-secret-846299 stream-config-secret-846300 canary\n"))
	if err := streamer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := streamer.ErrorOutput(); strings.Contains(got, "stream-env-secret-846299") ||
		strings.Contains(got, "stream-config-secret-846300") {
		t.Fatalf("combined redaction leaked secret: %q", got)
	}
	if got := streamer.ErrorOutput(); !strings.Contains(got, "canary") {
		t.Fatalf("expected a non-secret canary in captured output, got %q", got)
	}
}

func streamTestOutput(t *testing.T, input string, options StreamerOptions) []observerEntry {
	t.Helper()
	logs := InitTestObserved(t, slog.LevelDebug)
	streamer := NewJSONLogStreamer(options)
	_, err := streamer.Write([]byte(input))
	require.NoError(t, err)
	require.NoError(t, streamer.Close())

	entries := logs.All()
	output := make([]observerEntry, len(entries))
	for i, entry := range entries {
		output[i] = observerEntry{Level: entry.Level, Message: entry.Message}
	}
	return output
}

type observerEntry struct {
	Level   Level
	Message string
}

func TestJSONLogStreamerStructuredLogPreservesLevel(t *testing.T) {
	entries := streamTestOutput(
		t,
		`{"level":"debug","ts":"2026-09-01T08:27:30.002Z","msg":"received docker credentials post data: bytes=23"}`+"\n",
		StreamerOptions{FallbackLevel: slog.LevelInfo},
	)
	require.Len(t, entries, 1)
	assert.Equal(t, slog.LevelDebug, entries[0].Level)
	assert.Equal(t, "received docker credentials post data: bytes=23", entries[0].Message)
	assert.NotContains(t, entries[0].Message, `{"level":"debug"`)
}

func TestJSONLogStreamerSupportsMessageField(t *testing.T) {
	entries := streamTestOutput(
		t,
		`{"level":"warn","message":"example warning"}`+"\n",
		StreamerOptions{
			FallbackLevel: slog.LevelInfo,
		},
	)

	require.Len(t, entries, 1)
	assert.Equal(t, slog.LevelWarn, entries[0].Level)
	assert.Equal(t, "example warning", entries[0].Message)
}

func TestJSONLogStreamerUnknownLevelUsesFallback(t *testing.T) {
	line := `{"msg":"application JSON output"}`
	entries := streamTestOutput(
		t,
		line+"\n",
		StreamerOptions{FallbackLevel: slog.LevelInfo},
	)

	require.Len(t, entries, 1)
	assert.Equal(t, slog.LevelInfo, entries[0].Level)
	assert.Equal(t, line, entries[0].Message)
}

func TestJSONLogStreamerPlainTextUsesFallbackLevel(t *testing.T) {
	entries := streamTestOutput(
		t,
		"#12 [4/8] RUN apt-get update\n",
		StreamerOptions{
			FallbackLevel: slog.LevelInfo,
		},
	)
	require.Len(t, entries, 1)
	assert.Equal(t, slog.LevelInfo, entries[0].Level)
	assert.Equal(t, "#12 [4/8] RUN apt-get update", entries[0].Message)
}

func TestJSONLogStreamerMalformedJSONUsesFallbackLevel(t *testing.T) {
	line := `{"level":"debug","msg":`
	entries := streamTestOutput(
		t,
		line+"\n",
		StreamerOptions{FallbackLevel: slog.LevelInfo},
	)

	require.Len(t, entries, 1)
	assert.Equal(t, slog.LevelInfo, entries[0].Level)
	assert.Equal(t, line, entries[0].Message)
}

func TestJSONLogStreamerMixedStream(t *testing.T) {
	entries := streamTestOutput(
		t,
		strings.Join([]string{
			"#1 loading build definition",
			`{"level":"debug","msg":"received docker credentials post data: bytes=23"}`,
			"#2 building image",
			"",
		}, "\n"),
		StreamerOptions{FallbackLevel: slog.LevelInfo},
	)

	require.Len(t, entries, 3)
	assert.Equal(t, slog.LevelInfo, entries[0].Level)
	assert.Equal(t, "#1 loading build definition", entries[0].Message)
	assert.Equal(t, slog.LevelDebug, entries[1].Level)
	assert.Equal(t, "received docker credentials post data: bytes=23", entries[1].Message)
	assert.Equal(t, slog.LevelInfo, entries[2].Level)
	assert.Equal(t, "#2 building image", entries[2].Message)
}

func TestJSONLogStreamerNoDoubleWrappedStructuredOutput(t *testing.T) {
	entries := streamTestOutput(
		t,
		`{"level":"debug","msg":"credential request received"}`+"\n",
		StreamerOptions{
			FallbackLevel: slog.LevelInfo,
		},
	)

	require.Len(t, entries, 1)
	assert.Equal(t, slog.LevelDebug, entries[0].Level)
	assert.NotContains(t, entries[0].Message, `{"level":"debug"`)
}

func TestJSONLogStreamerTabDelimitedLevelPrefix(t *testing.T) {
	entries := streamTestOutput(
		t,
		"2026-09-01T08:27:30.002Z\tDEBUG\tcredential request received\n",
		StreamerOptions{
			FallbackLevel:       slog.LevelInfo,
			DetectLevelPrefixes: true,
		},
	)

	require.Len(t, entries, 1)
	assert.Equal(t, slog.LevelDebug, entries[0].Level)
	assert.Equal(
		t,
		"2026-09-01T08:27:30.002Z\tDEBUG\tcredential request received",
		entries[0].Message,
	)
}

func TestJSONLogStreamerCaptureLinesIsBounded(t *testing.T) {
	logs := InitTestObserved(t, slog.LevelDebug)
	streamer := NewJSONLogStreamer(StreamerOptions{
		FallbackLevel: slog.LevelInfo,
		CaptureLines:  1,
	})
	_, err := streamer.Write([]byte("first\nsecond\n"))
	require.NoError(t, err)
	require.NoError(t, streamer.Close())

	assert.Equal(t, "second", streamer.ErrorOutput())
	assert.Len(t, logs.All(), 2)
}

func TestJSONLogStreamerCaptureBytesIsBounded(t *testing.T) {
	const maxBytes = 16
	streamer := NewJSONLogStreamer(StreamerOptions{
		CaptureLines: 25,
		CaptureBytes: maxBytes,
	})
	_, err := streamer.Write([]byte("first line\nsecond line\n"))
	require.NoError(t, err)
	require.NoError(t, streamer.Close())

	assert.LessOrEqual(t, len(streamer.ErrorOutput()), maxBytes)
	assert.Equal(t, "second line", streamer.ErrorOutput())
}

func TestJSONLogStreamerCaptureBytesTruncatesOversizedLine(t *testing.T) {
	const maxBytes = 8
	streamer := NewJSONLogStreamer(StreamerOptions{CaptureBytes: maxBytes})
	_, err := streamer.Write([]byte("prefix-0123456789\n"))
	require.NoError(t, err)
	require.NoError(t, streamer.Close())

	assert.Equal(t, "23456789", streamer.ErrorOutput())
}

func TestJSONLogStreamerFormattedOutputHasNoNestedEnvelope(t *testing.T) {
	Init(Config{Level: LevelDebugName, Format: testFormatJSON})
	var output bytes.Buffer
	remove := AddSink(&output)
	defer remove()
	streamer := NewJSONLogStreamer(StreamerOptions{FallbackLevel: slog.LevelInfo})
	_, err := streamer.Write([]byte(`{"level":"debug","msg":"credential request received"}` + "\n"))
	require.NoError(t, err)
	require.NoError(t, streamer.Close())
	require.NoError(t, Sync())
	assert.Contains(t, output.String(), `"msg":"credential request received"`)
}

func TestJSONLogStreamerDrainsOversizedOutput(t *testing.T) {
	for _, mode := range []string{streamNextMode, "eof"} {
		t.Run(mode, func(t *testing.T) {
			logs := InitTestObserved(t, slog.LevelDebug)
			streamer := NewJSONLogStreamer(StreamerOptions{
				FallbackLevel: slog.LevelInfo,
				CaptureLines:  2,
				CaptureBytes:  256,
				Redactor:      redact.FromEntries([]string{"TOKEN=stream-boundary-secret"}),
			})
			streamer.LogLine(`{"level":"error","msg":"old","error_code":"old-code"}`)
			written := make(chan error, 1)
			go func() { written <- writeOversizedOutput(streamer, mode) }()
			select {
			case err := <-written:
				require.NoError(t, err)
			case <-time.After(15 * time.Second):
				_ = streamer.Close()
				<-written
				t.Fatal("oversized writer blocked")
			}
			require.NoError(t, streamer.Close())
			require.Empty(t, streamer.ErrorCode())
			entries := logs.All()
			require.GreaterOrEqual(t, len(entries), 2)
			require.Equal(t, "[oversized subprocess output truncated]", entries[1].Message)
			require.NotContains(t, entries[1].Message, "stream-boundary-")
			require.LessOrEqual(t, len(streamer.ErrorOutput()), 256)
			require.NotContains(t, streamer.ErrorOutput(), "stream-boundary-secret")
			require.NotContains(t, streamer.ErrorOutput(), "stream-boundary-")
			if mode == streamNextMode {
				require.Len(t, entries, 3)
				require.Equal(t, "next record", entries[2].Message)
				require.Equal(t, "canary", entries[2].AttrsMap()["component"])
				require.Equal(t, "***", entries[2].AttrsMap()["sensitive"])
			}
		})
	}
}

func writeOversizedOutput(writer io.Writer, mode string) error {
	chunk := strings.Repeat("x", 64*1024)
	for range 20 {
		if _, err := io.WriteString(writer, chunk); err != nil {
			return err
		}
	}
	for _, part := range []string{"stream-boundary-", "secret canary"} {
		if _, err := io.WriteString(writer, part); err != nil {
			return err
		}
	}
	if mode == streamNextMode {
		_, err := io.WriteString(writer, "\n"+`{"level":"info","msg":"next record",`+
			`"component":"canary","sensitive":"stream-boundary-secret"}`+"\n")
		return err
	}
	return nil
}

func TestJSONLogStreamerDrainsNormalUnterminatedRecord(t *testing.T) {
	logs := InitTestObserved(t, slog.LevelInfo)
	streamer := NewJSONLogStreamer(StreamerOptions{FallbackLevel: slog.LevelInfo, CaptureLines: 1})
	_, err := streamer.Write([]byte(`{"level":"info","msg":"unterminated","component":"canary"}`))
	require.NoError(t, err)
	require.NoError(t, streamer.Close())
	entries := logs.All()
	require.Len(t, entries, 1)
	require.Equal(t, "unterminated", entries[0].Message)
	require.Equal(t, "canary", entries[0].AttrsMap()["component"])
}
