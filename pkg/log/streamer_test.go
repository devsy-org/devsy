package log

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func streamTestOutput(t *testing.T, input string, options StreamerOptions) []observerEntry {
	t.Helper()
	logs := InitTestObserved(t, zapcore.DebugLevel)
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
	Level   zapcore.Level
	Message string
}

func TestJSONLogStreamerStructuredLogPreservesLevel(t *testing.T) {
	entries := streamTestOutput(
		t,
		`{"level":"debug","ts":"2026-09-01T08:27:30.002Z","msg":"received docker credentials post data: bytes=23"}`+"\n",
		StreamerOptions{FallbackLevel: LevelInfo},
	)
	require.Len(t, entries, 1)
	assert.Equal(t, zapcore.DebugLevel, entries[0].Level)
	assert.Equal(t, "received docker credentials post data: bytes=23", entries[0].Message)
	assert.NotContains(t, entries[0].Message, `{"level":"debug"`)
}

func TestJSONLogStreamerSupportsMessageField(t *testing.T) {
	entries := streamTestOutput(
		t,
		`{"level":"warn","message":"example warning"}`+"\n",
		StreamerOptions{
			FallbackLevel: LevelInfo,
		},
	)

	require.Len(t, entries, 1)
	assert.Equal(t, zapcore.WarnLevel, entries[0].Level)
	assert.Equal(t, "example warning", entries[0].Message)
}

func TestJSONLogStreamerUnknownLevelUsesFallback(t *testing.T) {
	line := `{"msg":"application JSON output"}`
	entries := streamTestOutput(
		t,
		line+"\n",
		StreamerOptions{FallbackLevel: LevelInfo},
	)

	require.Len(t, entries, 1)
	assert.Equal(t, zapcore.InfoLevel, entries[0].Level)
	assert.Equal(t, line, entries[0].Message)
}

func TestJSONLogStreamerPlainTextUsesFallbackLevel(t *testing.T) {
	entries := streamTestOutput(
		t,
		"#12 [4/8] RUN apt-get update\n",
		StreamerOptions{
			FallbackLevel: LevelInfo,
		},
	)
	require.Len(t, entries, 1)
	assert.Equal(t, zapcore.InfoLevel, entries[0].Level)
	assert.Equal(t, "#12 [4/8] RUN apt-get update", entries[0].Message)
}

func TestJSONLogStreamerMalformedJSONUsesFallbackLevel(t *testing.T) {
	line := `{"level":"debug","msg":`
	entries := streamTestOutput(
		t,
		line+"\n",
		StreamerOptions{FallbackLevel: LevelInfo},
	)

	require.Len(t, entries, 1)
	assert.Equal(t, zapcore.InfoLevel, entries[0].Level)
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
		StreamerOptions{FallbackLevel: LevelInfo},
	)

	require.Len(t, entries, 3)
	assert.Equal(t, zapcore.InfoLevel, entries[0].Level)
	assert.Equal(t, "#1 loading build definition", entries[0].Message)
	assert.Equal(t, zapcore.DebugLevel, entries[1].Level)
	assert.Equal(t, "received docker credentials post data: bytes=23", entries[1].Message)
	assert.Equal(t, zapcore.InfoLevel, entries[2].Level)
	assert.Equal(t, "#2 building image", entries[2].Message)
}

func TestJSONLogStreamerNoDoubleWrappedStructuredOutput(t *testing.T) {
	entries := streamTestOutput(
		t,
		`{"level":"debug","msg":"credential request received"}`+"\n",
		StreamerOptions{
			FallbackLevel: LevelInfo,
		},
	)

	require.Len(t, entries, 1)
	assert.Equal(t, zapcore.DebugLevel, entries[0].Level)
	assert.NotContains(t, entries[0].Message, `{"level":"debug"`)
}

func TestJSONLogStreamerCaptureLinesIsBounded(t *testing.T) {
	logs := InitTestObserved(t, zapcore.DebugLevel)
	streamer := NewJSONLogStreamer(StreamerOptions{
		FallbackLevel: LevelInfo,
		CaptureLines:  1,
	})
	_, err := streamer.Write([]byte("first\nsecond\n"))
	require.NoError(t, err)
	require.NoError(t, streamer.Close())

	assert.Equal(t, "second", streamer.ErrorOutput())
	assert.Len(t, logs.All(), 2)
}

func TestJSONLogStreamerFormattedOutputHasNoNestedEnvelope(t *testing.T) {
	var output bytes.Buffer
	previous := sugar.Load()
	encoderConfig := zap.NewDevelopmentEncoderConfig()
	encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder
	core := zapcore.NewCore(
		zapcore.NewConsoleEncoder(encoderConfig),
		zapcore.AddSync(&output),
		zapcore.DebugLevel,
	)
	sugar.Store(zap.New(core).Sugar())
	t.Cleanup(func() { sugar.Store(previous) })

	streamer := NewJSONLogStreamer(StreamerOptions{FallbackLevel: LevelInfo})
	_, err := streamer.Write([]byte(`{"level":"debug","msg":"credential request received"}` + "\n"))
	require.NoError(t, err)
	require.NoError(t, streamer.Close())

	assert.NotContains(t, output.String(), `INFO {"level":"debug"`)
	assert.Contains(t, output.String(), "DEBUG\tcredential request received")
}
