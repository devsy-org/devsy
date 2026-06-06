package cmd

import (
	"bytes"
	"strings"
	"sync"
	"testing"

	"github.com/devsy-org/devsy/pkg/log"
	"github.com/stretchr/testify/require"
)

// syncBuffer is a concurrency-safe bytes.Buffer. AddSink does not serialize
// writes, so test sinks must guard their own state.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// captureReportError mirrors Execute's path: run args through the root command
// and feed the resulting error to reportError, capturing the logger output.
func captureReportError(t *testing.T, args []string) (string, int) {
	t.Helper()
	log.Init(log.Config{Format: "text"})
	sink := &syncBuffer{}
	remove := log.AddSink(sink)
	defer remove()

	rootCmd, _ := BuildRoot()
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	require.Error(t, err)

	code := reportError(err, "text")
	// Sync flushes buffered entries; syncing os.Stderr returns a harmless
	// "invalid argument" on non-terminal fds, so the result is ignored.
	_ = log.Sync()
	return sink.String(), code
}

// TestUnknownCommand_PrintsError verifies an unknown subcommand reports an
// error through the logger instead of exiting silently.
func TestUnknownCommand_PrintsError(t *testing.T) {
	out, code := captureReportError(t, []string{"totally-bogus-command"})
	require.NotEmpty(t, strings.TrimSpace(out),
		"unknown command must produce error output, not exit silently")
	require.NotZero(t, code, "unknown command must exit non-zero")
}

// TestUnknownFlag_PrintsError verifies an unknown flag on a valid command
// reports an error rather than exiting silently.
func TestUnknownFlag_PrintsError(t *testing.T) {
	out, code := captureReportError(t, []string{cmdWorkspace, "list", "--definitely-not-a-flag"})
	require.NotEmpty(t, strings.TrimSpace(out),
		"unknown flag must produce error output, not exit silently")
	require.NotZero(t, code, "unknown flag must exit non-zero")
}

func TestParseLogOutputFlag(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"default when absent", []string{cmdWorkspace, "list"}, "text"},
		{"equals form", []string{"--log-output=json"}, "json"},
		{"space form", []string{"--log-output", "logfmt"}, "logfmt"},
		{"log-format alias", []string{"--log-format=json"}, "json"},
		{"trailing flag with no value falls back", []string{"--log-output"}, "text"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, parseLogOutputFlag(tc.args))
		})
	}
}
