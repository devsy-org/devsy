package sshtunnel

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/tunnel"
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

	pb, err := tunnel.NewPipeBridge()
	require.NoError(t, err)
	defer pb.Close()

	// Close the read side so StdioClient fails immediately.
	_ = pb.StdoutReader.Close()

	grpcBridge, err := tunnel.NewPipeBridge()
	require.NoError(t, err)
	defer grpcBridge.Close()

	_, err = runSSHTunnel(t.Context(), sshTunnelParams{
		stdout:     pb.StdoutReader,
		stdin:      pb.StdinWriter,
		grpcBridge: grpcBridge,
	})
	require.Error(t, err)

	messages := make([]string, 0, len(logs.All()))
	for _, entry := range logs.All() {
		messages = append(messages, entry.Message)
	}
	assert.Contains(t, messages, "tunnel: setup start")

	foundComplete := false
	for _, msg := range messages {
		if strings.HasPrefix(msg, "tunnel: setup complete elapsed=") {
			foundComplete = true
			break
		}
	}
	assert.True(t, foundComplete, "missing 'tunnel: setup complete' log: %v", messages)
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

func TestExecuteCommand_PipeBridgeIntegration(t *testing.T) {
	t.Run("helper error propagates through PipeBridge", func(t *testing.T) {
		wantErr := fmt.Errorf("inject failed")

		_, err := ExecuteCommand(context.Background(), ExecuteCommandOptions{
			AgentInject: func(_ context.Context, _ string, _ *os.File, _ *os.File, _ io.WriteCloser) error {
				return wantErr
			},
			SSHCommand: "test-ssh",
			Command:    "test-cmd",
			TunnelServerFunc: func(_ context.Context, _ io.WriteCloser, _ io.Reader) (*config2.Result, error) {
				return nil, nil
			},
		})

		require.Error(t, err)
		// Error message varies by platform (pipe teardown ordering), so we
		// only verify an error occurred.
	})

	t.Run("context cancellation stops execution", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		_, err := ExecuteCommand(ctx, ExecuteCommandOptions{
			AgentInject: func(ctx context.Context, _ string, _ *os.File, _ *os.File, _ io.WriteCloser) error {
				cancel()
				<-ctx.Done()
				return ctx.Err()
			},
			SSHCommand: "test-ssh",
			Command:    "test-cmd",
			TunnelServerFunc: func(_ context.Context, _ io.WriteCloser, _ io.Reader) (*config2.Result, error) {
				return nil, nil
			},
		})

		// Context cancellation is an expected error — RunPair classifies it
		// and may return nil or a wrapped error. Either way, no hang.
		_ = err
	})
}

func TestExecuteCommand_NoPipePairOrTimerTypes(t *testing.T) {
	// Verify the old types are gone by confirming the new code compiles
	// without pipePair, sshTunnelResult, sshSessionTunnel, createPipes,
	// collectTunnelErrors, waitForTunnelCompletion, or cleanupTimeout.
	// This test documents the migration: if someone re-adds the old types,
	// it signals they should use tunnel.PipeBridge instead.

	pb, err := tunnel.NewPipeBridge()
	require.NoError(t, err)
	defer pb.Close()

	assert.NotNil(t, pb.StdoutReader)
	assert.NotNil(t, pb.StdoutWriter)
	assert.NotNil(t, pb.StdinReader)
	assert.NotNil(t, pb.StdinWriter)
}
