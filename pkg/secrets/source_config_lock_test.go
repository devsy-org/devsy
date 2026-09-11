package secrets

import (
	"fmt"
	"sync"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/stretchr/testify/require"
)

const testSecretSourcePath = "test-secrets.enc.yaml"

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
			Path: testSecretSourcePath,
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

	final, err := LoadSourceConfigs(devsyConfig)
	require.NoError(t, err)
	require.Len(t, final, 1)
	require.Contains(t, wantSets, final)
}

func TestModifySourceConfigsSerializesConcurrentMutations(t *testing.T) {
	devsyConfig := &config.Config{
		DefaultContext: "default",
		Origin:         t.TempDir() + "/config.yaml",
	}

	const writers = 20
	var wg sync.WaitGroup
	errs := make([]error, writers)
	for i := range writers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = ModifySourceConfigs(
				devsyConfig,
				func(sources []SourceConfig) ([]SourceConfig, error) {
					return AddSourceConfig(sources, SourceConfig{
						Name: fmt.Sprintf("source-%d", i),
						Type: SOPSFormatter,
						Path: testSecretSourcePath,
					})
				},
			)
		}(i)
	}
	wg.Wait()

	for _, err := range errs {
		require.NoError(t, err)
	}

	final, err := LoadSourceConfigs(devsyConfig)
	require.NoError(t, err)
	require.Len(t, final, writers)
}
