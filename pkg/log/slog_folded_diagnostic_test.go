package log

import (
	"encoding/json"
	"testing"

	"github.com/devsy-org/devsy/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSlogMessagesMaskFoldedAuthorizationBeforeEncoding(t *testing.T) {
	const canary = "synthetic-folded-slog-value"
	const detail = "ordinary diagnostic detail"
	previous := current.Load()
	t.Cleanup(func() { current.Store(previous) })
	t.Cleanup(SuppressConsoleOutput())
	for _, format := range []string{string(logging.FormatText), testFormatJSON, testFormatLogfmt} {
		t.Run(format, func(t *testing.T) {
			Init(Config{Format: format})
			var sink syncBuffer
			t.Cleanup(AddSink(&sink))
			Slog().With("attempts", 7).WithGroup("transfer").Error(
				"Authorization:\n Bearer "+canary+"\n\n"+detail,
				"state", "queued",
			)
			require.NoError(t, Sync())
			out := sink.String()
			assert.NotContains(t, out, canary)
			assert.Contains(t, out, detail)
			assert.Contains(t, out, "attempts")
			assert.Contains(t, out, "queued")
			if format == testFormatJSON {
				assert.True(t, json.Valid([]byte(out)))
			}
		})
	}
}
