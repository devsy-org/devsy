package subprocess

import (
	"bytes"
	"io"
	"testing"

	"github.com/devsy-org/devsy/logging/redact"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiagnosticCaptureMasksFoldedAuthorization(t *testing.T) {
	const input = "Authorization:\r\n Bearer test-credential\r\n\r\n next public line\r\n"
	const want = "Authorization:\r\n***\r\n\r\n next public line\r\n"
	for _, size := range []int{len(input), 1} {
		redactor := redact.New(redact.Options{})
		captured := NewBuffer(redactor)
		var live bytes.Buffer
		writer := &StreamingRedactingWriter{Next: &live, Redactor: redactor}
		for start := 0; start < len(input); start += size {
			chunk := input[start:min(start+size, len(input))]
			for _, target := range []io.Writer{captured, writer} {
				n, err := io.WriteString(target, chunk)
				require.NoError(t, err, "write at %d", start)
				require.Equal(t, len(chunk), n, "write at %d", start)
			}
		}
		require.NoError(t, writer.Flush())
		assert.Equal(t, want, captured.String(), "bounded capture for chunk size %d", size)
		assert.Equal(t, want, live.String(), "live diagnostics for chunk size %d", size)
	}
}

func TestCompleteSubprocessDiagnosticsMaskFoldedAuthorization(t *testing.T) {
	const canary = "synthetic-folded-argument-value"
	const input = "Authorization:\n Bearer " + canary + "\n\npublic diagnostic"
	redactor := redact.FromEntries(nil)

	t.Run("display command", func(t *testing.T) {
		text := displayCommand("fixture-command", []string{input, "public argument"}, redactor)
		assert.NotContains(t, text, canary)
		assert.Contains(t, text, "public diagnostic")
		assert.Contains(t, text, `"public argument"`)
	})
	t.Run("complete writer", func(t *testing.T) {
		var destination bytes.Buffer
		writer := RedactingWriter{Next: &destination, Redactor: redactor}
		count, err := io.WriteString(writer, input)
		require.NoError(t, err)
		assert.Equal(t, len(input), count)
		assert.NotContains(t, destination.String(), canary)
		assert.Contains(t, destination.String(), "public diagnostic")
	})
}
