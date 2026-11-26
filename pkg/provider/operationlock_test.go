package provider

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLockProviderHoldsLockUntilUnlock(t *testing.T) {
	useTempDevsyHome(t)

	probe, err := GetProviderOperationLock("default", "same")
	require.NoError(t, err)

	unlock, err := LockProvider("default", "same")
	require.NoError(t, err)

	held, err := probe.TryLock()
	require.NoError(t, err)
	require.False(t, held, "operation lock must be held until unlock is called")

	unlock()

	acquired, err := probe.TryLock()
	require.NoError(t, err)
	require.True(t, acquired, "unlock must release the operation lock")
	require.NoError(t, probe.Unlock())
}

func TestLockProviderWrapsGetError(t *testing.T) {
	useTempDevsyHome(t)

	unlock, err := LockProvider("default", "")
	require.EqualError(t, err, `get operation lock: invalid provider name ""`)
	require.Nil(t, unlock)
}

func TestLockProviderNamedIncludesNameInError(t *testing.T) {
	useTempDevsyHome(t)

	unlock, err := LockProviderNamed("default", "")
	require.EqualError(t, err, `get operation lock for : invalid provider name ""`)
	require.Nil(t, unlock)
}
