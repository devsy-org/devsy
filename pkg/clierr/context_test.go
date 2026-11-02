package clierr

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const runKey = "runId"

func TestWithContextAddsKeysAndKeepsChain(t *testing.T) {
	root := errors.New("hook failed")

	got := WithContext(root, map[string]string{runKey: "r1"})

	require.ErrorIs(t, got, root)
	classified := Classify(got)
	assert.Equal(t, "r1", classified.Context[runKey])
	assert.Equal(t, "hook failed", classified.Message)
}

func TestWithContextKeepsExistingKeysAndNoopsWithoutValues(t *testing.T) {
	base := &CLIError{Code: CodeUnknown, Message: "m", Context: map[string]string{runKey: "kept"}}

	got := Classify(WithContext(base, map[string]string{runKey: "new", "extra": "x"}))

	assert.Equal(t, "kept", got.Context[runKey])
	assert.Equal(t, "x", got.Context["extra"])
	assert.Equal(t, base, WithContext(base, nil))
	assert.NoError(t, WithContext(nil, map[string]string{"a": "b"}))
}
