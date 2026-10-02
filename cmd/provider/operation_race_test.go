package provider

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
)

func setupProviderOperationTest(t *testing.T) *config.Config {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	config.ResetPathManager()
	t.Cleanup(config.ResetPathManager)
	initial := &config.Config{
		DefaultContext: "default",
		Contexts: map[string]*config.ContextConfig{
			"default": {Providers: map[string]*config.ProviderConfig{}},
		},
	}
	require.NoError(t, config.SaveConfig(initial))
	return initial
}

func TestConcurrentAddsDoNotOverwriteProvider(t *testing.T) {
	initial := setupProviderOperationTest(t)
	path := filepath.Join(t.TempDir(), "provider.yaml")
	require.NoError(
		t,
		os.WriteFile(path, []byte("name: same\nexec:\n  command: ['true']\n"), 0o600),
	)

	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			cmd := &AddCmd{GlobalFlags: &flags.GlobalFlags{}, Use: false}
			results <- cmd.Run(context.Background(), initial, []string{path})
		})
	}
	wg.Wait()
	close(results)
	var succeeded, failed int
	for err := range results {
		if err == nil {
			succeeded++
		} else {
			failed++
			require.ErrorContains(t, err, "already exists")
		}
	}
	require.Equal(t, 1, succeeded)
	require.Equal(t, 1, failed)
	stored, err := config.LoadConfig("default", "")
	require.NoError(t, err)
	require.NotNil(t, stored.Current().Providers["same"])
	_, err = provider2.LoadProviderConfig("default", "same")
	require.NoError(t, err)
}

func TestUseWaitsForProviderDeletion(t *testing.T) {
	initial := setupProviderOperationTest(t)
	require.NoError(t, provider2.SaveProviderConfig("default", &provider2.ProviderConfig{
		Name: "same",
		Exec: provider2.ProviderCommands{Command: []string{"true"}},
	}))
	lock, err := provider2.GetProviderOperationLock("default", "same")
	require.NoError(t, err)
	require.NoError(t, lock.Lock())
	t.Cleanup(func() { _ = lock.Unlock() })

	result := make(chan error, 1)
	go func() { result <- UseProvider("default", "", "same") }()
	select {
	case err := <-result:
		t.Fatalf("provider use completed during deletion: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	require.NoError(t, DeleteProviderConfig(initial, "same", false))
	require.NoError(t, lock.Unlock())
	require.ErrorContains(t, <-result, "not found")
	stored, err := config.LoadConfig("default", "")
	require.NoError(t, err)
	require.Empty(t, stored.Current().DefaultProvider)
}
