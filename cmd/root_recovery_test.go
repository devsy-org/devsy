package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/stretchr/testify/require"
)

const (
	startupRecoveryContextName = "staging"
	startupContextCommandName  = "context"
)

func TestStartupFencesInterruptedContextDeletion(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	t.Setenv(config.EnvHome, home)
	t.Setenv(config.EnvConfig, "")
	configPath, err := config.GetConfigPath()
	require.NoError(t, err)
	fingerprint, err := config.ContextDeletionFingerprint(&config.ContextConfig{})
	require.NoError(t, err)
	dataDirectory, err := config.ContextDeletionDataDirectory()
	require.NoError(t, err)
	require.NoError(t, config.WriteContextDeletionIntent(&config.ContextDeletionIntent{
		SchemaVersion:  1,
		ConfigFile:     configPath,
		StoreDirectory: filepath.Dir(configPath),
		DataDirectory:  dataDirectory,
		Context:        startupRecoveryContextName,
		Fingerprint:    fingerprint,
	}))
	// Startup detection must not inspect even an unreadable encrypted store.
	blobPath := filepath.Join(filepath.Dir(configPath), "secrets.enc")
	require.NoError(t, os.WriteFile(blobPath, []byte("not an encrypted store"), 0o600))
	root, _ := BuildRoot()
	envList, _, err := root.Find([]string{"env", "list"})
	require.NoError(t, err)
	err = root.PersistentPreRunE(envList, nil)
	require.ErrorIs(t, err, config.ErrContextDeletionPending)
	require.Contains(t, err.Error(), `context delete "staging"`)
	// The delete command's own lock validates the exact recovery target.
	resume, _, err := root.Find(
		[]string{startupContextCommandName, "rm", startupRecoveryContextName},
	)
	require.NoError(t, err)
	resume.SetContext(context.Background())
	require.NoError(t, root.PersistentPreRunE(resume, []string{startupRecoveryContextName}))
	_, err = config.LockConfigForContextDeletion("other")
	require.ErrorIs(t, err, config.ErrContextDeletionPending)
	// #nosec G304 -- fixture path under the test's temporary config directory.
	blob, err := os.ReadFile(blobPath)
	require.NoError(t, err)
	require.Equal(t, "not an encrypted store", string(blob))
}

func TestStartupRejectsMalformedDeletionIntentWithoutLeakingContent(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())
	t.Setenv(config.EnvConfig, "")
	configPath, err := config.GetConfigPath()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(configPath), 0o700))
	intentPath := filepath.Join(filepath.Dir(configPath), config.ContextDeletionIntentFile)
	require.NoError(t, os.WriteFile(intentPath, []byte(`{"secret":"private-fixture-value"`), 0o600))
	root, _ := BuildRoot()
	list, _, err := root.Find([]string{startupContextCommandName, "list"})
	require.NoError(t, err)
	err = root.PersistentPreRunE(list, nil)
	require.ErrorIs(t, err, config.ErrContextDeletionIntentInvalid)
	require.NotContains(t, err.Error(), "private-fixture-value")
}
