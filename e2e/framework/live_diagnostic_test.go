package framework

import (
	"bytes"
	"testing"

	"github.com/devsy-org/devsy/logging/redact"
	"github.com/devsy-org/devsy/pkg/livetee"
	"github.com/stretchr/testify/assert"
)

func TestLiveDiagnosticCaptureIsChunkIndependent(t *testing.T) {
	const input = "[INFO] start\n{\n  \"Cookie\": \"chunk-cookie-fixture\",\n" +
		"  \"note\": \"public detail\"\n}\nprompt: \n{\"Cookie\":\"chunk-compact-fixture\"}\ntail"
	run := func(size int) string {
		var live bytes.Buffer
		capture := newLiveDiagnosticCapture(
			livetee.New(&bytes.Buffer{}, &live),
			redact.New(redact.Options{}),
		)
		for start := 0; start < len(input); start += size {
			end := min(start+size, len(input))
			_, _ = capture.Write([]byte(input[start:end]))
		}
		capture.close()
		return live.String()
	}
	whole := run(len(input))
	assert.NotContains(t, whole, "chunk-cookie-fixture")
	assert.NotContains(t, whole, "chunk-compact-fixture")
	assert.Contains(t, whole, "public detail")
	assert.Contains(t, whole, "[INFO] start\n")
	assert.Contains(t, whole, "prompt: \n")
	assert.Contains(t, whole, "tail")
	for _, size := range []int{1, 2, 3, 7, 16} {
		assert.Equal(t, whole, run(size), "chunk size %d", size)
	}
}
