package framework

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOverrideEnvRestoresUnsetAfterSet(t *testing.T) {
	const name = "DEVSY_E2E_TEST_ENV_UNSET_THEN_SET"
	require.NoError(t, os.Unsetenv(name))

	restore, err := overrideEnv(name, new("value"))
	require.NoError(t, err)
	assert.Equal(t, "value", os.Getenv(name))

	restore()
	_, present := os.LookupEnv(name)
	assert.False(t, present, "unset prior state must come back as unset, not empty")
}

func TestOverrideEnvRestoresEmptyAfterSet(t *testing.T) {
	const name = "DEVSY_E2E_TEST_ENV_EMPTY_THEN_SET"
	t.Setenv(name, "")

	restore, err := overrideEnv(name, new("value"))
	require.NoError(t, err)
	assert.Equal(t, "value", os.Getenv(name))

	restore()
	got, present := os.LookupEnv(name)
	assert.True(t, present, "set-to-empty prior state must stay set")
	assert.Empty(t, got)
}

func TestOverrideEnvRestoresPreviousValue(t *testing.T) {
	const name = "DEVSY_E2E_TEST_ENV_VALUE_THEN_SET"
	t.Setenv(name, "original")

	restore, err := overrideEnv(name, new("replacement"))
	require.NoError(t, err)
	assert.Equal(t, "replacement", os.Getenv(name))

	restore()
	assert.Equal(t, "original", os.Getenv(name))
}

func TestOverrideEnvUnsetRestoresPreviousValue(t *testing.T) {
	const name = "DEVSY_E2E_TEST_ENV_VALUE_THEN_UNSET"
	t.Setenv(name, "original")

	restore, err := overrideEnv(name, nil)
	require.NoError(t, err)
	_, present := os.LookupEnv(name)
	assert.False(t, present, "nil value must unset the variable")

	restore()
	assert.Equal(t, "original", os.Getenv(name))
}
