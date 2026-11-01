package dependencytest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gofrs/flock"
	"github.com/stretchr/testify/require"
)

func TestHeldFlockOutranksStalePID(t *testing.T) {
	for _, kind := range []string{"operations", "leases"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			metadata := filepath.Join(root, kind+".json")
			require.NoError(
				t,
				os.WriteFile(metadata, []byte(`{"pid":-1,"state":"running"}`), 0o600),
			)
			owner, contender := flock.New(
				filepath.Join(root, "live.lock"),
			), flock.New(
				filepath.Join(root, "live.lock"),
			)
			t.Cleanup(
				func() { require.NoError(t, owner.Close()); require.NoError(t, contender.Close()) },
			)
			locked, err := owner.TryLock()
			require.NoError(t, err)
			require.True(t, locked)
			locked, err = contender.TryLock()
			require.NoError(t, err)
			require.False(t, locked)
			require.NoError(t, owner.Unlock())
			locked, err = contender.TryLock()
			require.NoError(t, err)
			require.True(t, locked)
			require.FileExists(t, metadata)
		})
	}
}
