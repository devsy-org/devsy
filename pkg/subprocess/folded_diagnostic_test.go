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
