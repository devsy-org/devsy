package secrets

import (
	"fmt"
	"sync"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/stretchr/testify/require"
)

// TestSaveSourceConfigsSerializesConcurrentWriters is a regression test for a
// race where concurrent `devsy secrets source add`/`remove` invocations could
// interleave their read-modify-write of secret-sources.yaml with no
// cross-process locking, risking a corrupted or torn write. SaveSourceConfigs
// now holds the same acquireFlock lock used elsewhere in this package (e.g.
// the local secret store) across its load-modify-write sequence, so
// concurrent writers are fully serialized: every write is exactly one
// caller's complete, well-formed source list, never an interleaved mix.
func TestSaveSourceConfigsSerializesConcurrentWriters(t *testing.T) {
	devsyConfig := &config.Config{
		DefaultContext: "default",
		Origin:         t.TempDir() + "/config.yaml",
	}

	const writers = 20
	var wg sync.WaitGroup
	errs := make([]error, writers)
	wantSets := make([][]SourceConfig, writers)
	for i := range writers {
		wantSets[i] = []SourceConfig{{
			Name: fmt.Sprintf("source-%d", i),
			Type: SOPSFormatter,
			Path: "secrets.enc.yaml",
		}}
	}
	for i := range writers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = SaveSourceConfigs(devsyConfig, wantSets[i])
		}(i)
	}
	wg.Wait()

	for _, err := range errs {
		require.NoError(t, err)
	}

	// Whichever writer finishes last determines the final state; the file
	// must equal exactly one writer's complete list, never a corrupted
	// interleaving of two or more.
	final, err := LoadSourceConfigs(devsyConfig)
	require.NoError(t, err)
	require.Len(t, final, 1)
	require.Contains(t, wantSets, final)
}
