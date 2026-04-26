package sshtunnel

import (
	"context"
	"testing"

	"github.com/devsy-org/devsy/pkg/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
)

func TestLogLine_JSONPassthrough(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMsg   string
		wantLevel zapcore.Level
	}{
		{
			name:      "json with message field",
			input:     `{"level":"info","message":"agent started"}`,
			wantMsg:   "agent started",
			wantLevel: zapcore.InfoLevel,
		},
		{
			name:      "json with msg field",
			input:     `{"level":"warn","msg":"disk nearly full"}`,
			wantMsg:   "disk nearly full",
			wantLevel: zapcore.WarnLevel,
		},
		{
			name:      "json error level",
			input:     `{"level":"error","message":"connection lost"}`,
			wantMsg:   "connection lost",
			wantLevel: zapcore.ErrorLevel,
		},
		{
			name:      "json debug level",
			input:     `{"level":"debug","message":"heartbeat sent"}`,
			wantMsg:   "heartbeat sent",
			wantLevel: zapcore.DebugLevel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := log.InitTestObserved(t, zapcore.DebugLevel)
			streamer := &TunnelLogStreamer{}
			streamer.logLine(tt.input)

			entries := logs.All()
			require.Len(t, entries, 1)
			assert.Equal(t, tt.wantMsg, entries[0].Message)
			assert.Equal(t, tt.wantLevel, entries[0].Level)
		})
	}
}

func TestLogLine_JSONLevelNormalization(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMsg   string
		wantLevel zapcore.Level
	}{
		{
			name:      "trace maps to debug",
			input:     `{"level":"trace","message":"trace event"}`,
			wantMsg:   "trace event",
			wantLevel: zapcore.DebugLevel,
		},
		{
			name:      "warning maps to warn",
			input:     `{"level":"warning","message":"deprecated call"}`,
			wantMsg:   "deprecated call",
			wantLevel: zapcore.WarnLevel,
		},
		{
			name:      "fatal maps to error",
			input:     `{"level":"fatal","message":"panic recovered"}`,
			wantMsg:   "panic recovered",
			wantLevel: zapcore.ErrorLevel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := log.InitTestObserved(t, zapcore.DebugLevel)
			streamer := &TunnelLogStreamer{}
			streamer.logLine(tt.input)

			entries := logs.All()
			require.Len(t, entries, 1)
			assert.Equal(t, tt.wantMsg, entries[0].Message)
			assert.Equal(t, tt.wantLevel, entries[0].Level)
		})
	}
}

func TestLogLine_PlainText(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMsg   string
		wantLevel zapcore.Level
	}{
		{
			name:      "timestamped text with level",
			input:     "2024-01-01T00:00:00Z info some message here",
			wantMsg:   "2024-01-01T00:00:00Z info some message here",
			wantLevel: zapcore.InfoLevel,
		},
		{
			name:      "plain text without level falls back to debug",
			input:     "just a plain line",
			wantMsg:   "just a plain line",
			wantLevel: zapcore.DebugLevel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := log.InitTestObserved(t, zapcore.DebugLevel)
			streamer := &TunnelLogStreamer{}
			streamer.logLine(tt.input)

			entries := logs.All()
			require.Len(t, entries, 1)
			assert.Equal(t, tt.wantMsg, entries[0].Message)
			assert.Equal(t, tt.wantLevel, entries[0].Level)
		})
	}
}

func TestLogLine_EmptyAndWhitespace(t *testing.T) {
	logs := log.InitTestObserved(t, zapcore.DebugLevel)
	streamer := &TunnelLogStreamer{}

	streamer.logLine("")
	streamer.logLine("   ")
	streamer.logLine("\r\n")

	assert.Empty(t, logs.All())
}

func TestLogLine_JSONWithoutMessage(t *testing.T) {
	logs := log.InitTestObserved(t, zapcore.DebugLevel)
	streamer := &TunnelLogStreamer{}

	streamer.logLine(`{"level":"info","key":"value"}`)

	entries := logs.All()
	require.Len(t, entries, 1)
	assert.Equal(t, zapcore.DebugLevel, entries[0].Level)
}

func TestExtractLogLevel(t *testing.T) {
	tests := []struct {
		input     string
		wantMatch bool
		wantLevel string
	}{
		{"2024-01-01T00:00:00Z debug foo", true, "debug"},
		{"2024-01-01T00:00:00Z info bar", true, "info"},
		{"2024-01-01T00:00:00Z warn baz", true, "warn"},
		{"2024-01-01T00:00:00Z error qux", true, "error"},
		{"2024-01-01T00:00:00Z fatal crash", true, "fatal"},
		{"no-colon info msg", false, ""},
		{"plain text", false, ""},
		{"ts: unknown msg", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			matched, level := extractLogLevel(tt.input)
			assert.Equal(t, tt.wantMatch, matched)
			assert.Equal(t, tt.wantLevel, level)
		})
	}
}

func TestRunSSHTunnel_TimingLogs(t *testing.T) {
	logs := log.InitTestObserved(t, zapcore.DebugLevel)

	pipes, err := createPipes()
	require.NoError(t, err)
	// Close the read side so StdioClient fails immediately.
	_ = pipes.stdoutReader.Close()

	ts := &sshSessionTunnel{
		sshPipes:  pipes,
		grpcPipes: &pipePair{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	result := runSSHTunnel(ctx, cancel, ts)
	require.Error(t, result.err)

	messages := make([]string, 0, len(logs.All()))
	for _, entry := range logs.All() {
		messages = append(messages, entry.Message)
	}
	assert.Contains(t, messages, "tunnel: setup start")
}

func TestNormalizeLevel(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"trace", "debug"},
		{"DEBUG", "debug"},
		{"info", "info"},
		{"INFO", "info"},
		{"warning", "warn"},
		{"warn", "warn"},
		{"WARN", "warn"},
		{"error", "error"},
		{"panic", "error"},
		{"fatal", "error"},
		{"unknown", "debug"},
		{"", "debug"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.want, normalizeLevel(tt.input))
		})
	}
}
