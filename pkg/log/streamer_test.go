package log

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	streamNextMode       = "next"
	streamHelperSelector = "-test.run=^TestJSONLogStreamerDrainsOversizedHelper$"
)

func TestJSONLogStreamerRedactsCapturedLines(t *testing.T) {
	streamer := NewJSONLogStreamer(StreamerOptions{
		FallbackLevel: SlogLevelInfo,
		CaptureLines:  2,
		Redactor:      secrets.NewRedactor([]string{"TOKEN=DEVSY_STREAM_SECRET_846297"}),
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
		FallbackLevel: SlogLevelInfo,
		CaptureLines:  1,
		Redactor:      secrets.NewRedactor([]string{"TOKEN=stream-config-secret-846300"}),
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
	logs := InitTestObserved(t, SlogLevelDebug)
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
		StreamerOptions{FallbackLevel: SlogLevelInfo},
	)
	require.Len(t, entries, 1)
	assert.Equal(t, SlogLevelDebug, entries[0].Level)
	assert.Equal(t, "received docker credentials post data: bytes=23", entries[0].Message)
	assert.NotContains(t, entries[0].Message, `{"level":"debug"`)
}

func TestJSONLogStreamerSupportsMessageField(t *testing.T) {
	entries := streamTestOutput(
		t,
		`{"level":"warn","message":"example warning"}`+"\n",
		StreamerOptions{
			FallbackLevel: SlogLevelInfo,
		},
	)

	require.Len(t, entries, 1)
	assert.Equal(t, SlogLevelWarn, entries[0].Level)
	assert.Equal(t, "example warning", entries[0].Message)
}

func TestJSONLogStreamerUnknownLevelUsesFallback(t *testing.T) {
	line := `{"msg":"application JSON output"}`
	entries := streamTestOutput(
		t,
		line+"\n",
		StreamerOptions{FallbackLevel: SlogLevelInfo},
	)

	require.Len(t, entries, 1)
	assert.Equal(t, SlogLevelInfo, entries[0].Level)
	assert.Equal(t, line, entries[0].Message)
}

func TestJSONLogStreamerPlainTextUsesFallbackLevel(t *testing.T) {
	entries := streamTestOutput(
		t,
		"#12 [4/8] RUN apt-get update\n",
		StreamerOptions{
			FallbackLevel: SlogLevelInfo,
		},
	)
	require.Len(t, entries, 1)
	assert.Equal(t, SlogLevelInfo, entries[0].Level)
	assert.Equal(t, "#12 [4/8] RUN apt-get update", entries[0].Message)
}

func TestJSONLogStreamerMalformedJSONUsesFallbackLevel(t *testing.T) {
	line := `{"level":"debug","msg":`
	entries := streamTestOutput(
		t,
		line+"\n",
		StreamerOptions{FallbackLevel: SlogLevelInfo},
	)

	require.Len(t, entries, 1)
	assert.Equal(t, SlogLevelInfo, entries[0].Level)
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
		StreamerOptions{FallbackLevel: SlogLevelInfo},
	)

	require.Len(t, entries, 3)
	assert.Equal(t, SlogLevelInfo, entries[0].Level)
	assert.Equal(t, "#1 loading build definition", entries[0].Message)
	assert.Equal(t, SlogLevelDebug, entries[1].Level)
	assert.Equal(t, "received docker credentials post data: bytes=23", entries[1].Message)
	assert.Equal(t, SlogLevelInfo, entries[2].Level)
	assert.Equal(t, "#2 building image", entries[2].Message)
}

func TestJSONLogStreamerNoDoubleWrappedStructuredOutput(t *testing.T) {
	entries := streamTestOutput(
		t,
		`{"level":"debug","msg":"credential request received"}`+"\n",
		StreamerOptions{
			FallbackLevel: SlogLevelInfo,
		},
	)

	require.Len(t, entries, 1)
	assert.Equal(t, SlogLevelDebug, entries[0].Level)
	assert.NotContains(t, entries[0].Message, `{"level":"debug"`)
}

func TestJSONLogStreamerTabDelimitedLevelPrefix(t *testing.T) {
	entries := streamTestOutput(
		t,
		"2026-09-01T08:27:30.002Z\tDEBUG\tcredential request received\n",
		StreamerOptions{
			FallbackLevel:       SlogLevelInfo,
			DetectLevelPrefixes: true,
		},
	)

	require.Len(t, entries, 1)
	assert.Equal(t, SlogLevelDebug, entries[0].Level)
	assert.Equal(
		t,
		"2026-09-01T08:27:30.002Z\tDEBUG\tcredential request received",
		entries[0].Message,
	)
}

func TestJSONLogStreamerCaptureLinesIsBounded(t *testing.T) {
	logs := InitTestObserved(t, SlogLevelDebug)
	streamer := NewJSONLogStreamer(StreamerOptions{
		FallbackLevel: SlogLevelInfo,
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
	streamer := NewJSONLogStreamer(StreamerOptions{FallbackLevel: SlogLevelInfo})
	_, err := streamer.Write([]byte(`{"level":"debug","msg":"credential request received"}` + "\n"))
	require.NoError(t, err)
	require.NoError(t, streamer.Close())
	require.NoError(t, Sync())
	assert.Contains(t, output.String(), `"msg":"credential request received"`)
}

func TestJSONLogStreamerDrainsOversizedHelper(t *testing.T) {
	if mode := os.Getenv("DEVSY_STREAM_HELPER_MODE"); mode != "" {
		writeOversizedHelper(mode)
		return
	}
	for _, mode := range []string{streamNextMode, "eof"} {
		t.Run(mode, func(t *testing.T) {
			logs := InitTestObserved(t, SlogLevelDebug)
			streamer := NewJSONLogStreamer(StreamerOptions{
				FallbackLevel: SlogLevelInfo,
				CaptureLines:  2,
				CaptureBytes:  256,
				Redactor:      secrets.NewRedactor([]string{"TOKEN=stream-boundary-secret"}),
			})
			streamer.LogLine(`{"level":"error","msg":"old","error_code":"old-code"}`)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], streamHelperSelector)
			cmd.Env = append(os.Environ(), "DEVSY_STREAM_HELPER_MODE="+mode)
			cmd.Stderr = streamer
			err := cmd.Run()
			closeErr := streamer.Close()
			require.NoError(t, err)
			require.NoError(t, closeErr)
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

func writeOversizedHelper(mode string) {
	chunk := strings.Repeat("x", 64*1024)
	for range 20 {
		if _, err := os.Stderr.WriteString(chunk); err != nil {
			os.Exit(2)
		}
	}
	for _, part := range []string{"stream-boundary-", "secret canary"} {
		if _, err := os.Stderr.WriteString(part); err != nil {
			os.Exit(2)
		}
	}
	if mode == streamNextMode {
		if _, err := os.Stderr.WriteString("\n" + `{"level":"info","msg":"next record",` +
			`"component":"canary","sensitive":"stream-boundary-secret"}` + "\n"); err != nil {
			os.Exit(2)
		}
	}
}

func TestJSONLogStreamerDrainsNormalUnterminatedRecord(t *testing.T) {
	logs := InitTestObserved(t, SlogLevelInfo)
	streamer := NewJSONLogStreamer(StreamerOptions{FallbackLevel: SlogLevelInfo, CaptureLines: 1})
	_, err := streamer.Write([]byte(`{"level":"info","msg":"unterminated","component":"canary"}`))
	require.NoError(t, err)
	require.NoError(t, streamer.Close())
	entries := logs.All()
	require.Len(t, entries, 1)
	require.Equal(t, "unterminated", entries[0].Message)
	require.Equal(t, "canary", entries[0].AttrsMap()["component"])
}
