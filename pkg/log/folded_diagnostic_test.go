package log

import (
	"io"
	"os"
	"testing"

	"github.com/devsy-org/devsy/logging/redact"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	foldedLogCanary = "synthetic-folded-log-value"
	foldedLogPublic = "folded-public-diagnostic"
)

func TestCompleteLogSinksMaskFoldedAuthorization(t *testing.T) {
	stderr := captureFoldedStderr(t)
	var sink syncBuffer
	t.Cleanup(AddSink(&sink))
	Init(Config{Format: "text"})
	Error("Authorization:\n Bearer " + foldedLogCanary + "\n\n" + foldedLogPublic)
	require.NoError(t, Sync())
	assertFoldedLogSinks(t, stderr, &sink)
}

func TestPassthroughSinksMaskFoldedAuthorization(t *testing.T) {
	stderr := captureFoldedStderr(t)
	var sink syncBuffer
	t.Cleanup(AddSink(&sink))
	writer := PassthroughWriterWithRedactor(redact.FromEntries(nil))
	for _, part := range []string{"Authorization:\r\n", " Bearer " + foldedLogCanary, "\r\n\r\n" + foldedLogPublic} {
		count, err := io.WriteString(writer, part)
		require.NoError(t, err)
		assert.Equal(t, len(part), count)
	}
	require.NoError(t, writer.Close())
	assertFoldedLogSinks(t, stderr, &sink)
}

func captureFoldedStderr(t *testing.T) *os.File {
	t.Helper()
	oldStderr, oldLogger := os.Stderr, current.Load()
	file, err := os.CreateTemp(t.TempDir(), "stderr-*.log")
	require.NoError(t, err)
	os.Stderr = file
	t.Cleanup(func() {
		os.Stderr = oldStderr
		current.Store(oldLogger)
		require.NoError(t, file.Close())
	})
	return file
}

func assertFoldedLogSinks(t *testing.T, stderr *os.File, sink *syncBuffer) {
	t.Helper()
	_, err := stderr.Seek(0, io.SeekStart)
	require.NoError(t, err)
	output, err := io.ReadAll(stderr)
	require.NoError(t, err)
	for _, record := range []string{string(output), sink.String()} {
		assert.NotContains(t, record, foldedLogCanary)
		assert.Contains(t, record, foldedLogPublic)
	}
}
