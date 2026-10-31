package clierr

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithContextAddsKeysAndKeepsChain(t *testing.T) {
	root := errors.New("hook failed")

	got := WithContext(root, map[string]string{"runId": "r1"})

	require.ErrorIs(t, got, root)
	classified := Classify(got)
	assert.Equal(t, "r1", classified.Context["runId"])
	assert.Equal(t, "hook failed", classified.Message)
}

func TestWithContextKeepsExistingKeysAndNoopsWithoutValues(t *testing.T) {
	base := &CLIError{Code: CodeUnknown, Message: "m", Context: map[string]string{"runId": "kept"}}

	got := Classify(WithContext(base, map[string]string{"runId": "new", "extra": "x"}))

	assert.Equal(t, "kept", got.Context["runId"])
	assert.Equal(t, "x", got.Context["extra"])
	assert.Equal(t, base, WithContext(base, nil))
	assert.NoError(t, WithContext(nil, map[string]string{"a": "b"}))
}
