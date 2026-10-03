package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const localContextDeletionTestWindows = "windows"

func contextDeletionConfigFixture(t *testing.T) *ContextDeletionIntent {
	t.Helper()
	ResetPathManager()
	t.Cleanup(ResetPathManager)
	t.Setenv(EnvHome, t.TempDir())
	t.Setenv(EnvConfig, "")
	path, err := GetConfigPath()
	require.NoError(t, err)
	fingerprint, err := ContextDeletionFingerprint(&ContextConfig{})
	require.NoError(t, err)
	dataDirectory, err := ContextDeletionDataDirectory()
	require.NoError(t, err)
	return &ContextDeletionIntent{
		SchemaVersion:  1,
		ConfigFile:     path,
		StoreDirectory: filepath.Dir(path),
		DataDirectory:  dataDirectory,
		Context:        "staging",
		Fingerprint:    fingerprint,
		Secrets:        []ContextDeletionSecret{{Name: "TOKEN", Backend: "file"}},
		EnvNames:       []string{"PLAIN"},
	}
}

func TestContextDeletionIntentFencesConfigMutationsAndWrongTargets(t *testing.T) {
	intent := contextDeletionConfigFixture(t)
	require.NoError(t, WriteContextDeletionIntent(intent))
	require.ErrorIs(t, CheckPendingContextDeletion(), ErrContextDeletionPending)
	_, err := LockConfig()
	require.ErrorIs(t, err, ErrContextDeletionPending)
	_, err = LockConfigForContextDeletion("other")
	require.ErrorIs(t, err, ErrContextDeletionPending)
	unlock, err := LockConfigForContextDeletion("staging")
	require.NoError(t, err)
	unlock()
	path := filepath.Join(filepath.Dir(intent.ConfigFile), ContextDeletionIntentFile)
	info, err := os.Stat(path)
	require.NoError(t, err)
	if runtime.GOOS != localContextDeletionTestWindows {
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
	require.NoError(t, ClearContextDeletionIntent())
	unlock, err = LockConfig()
	require.NoError(t, err)
	unlock()
}

func TestContextDeletionIntentRejectsUnsafeNamesAndWrongRoot(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*ContextDeletionIntent)
	}{
		{"traversal", func(i *ContextDeletionIntent) { i.Context = "../private-fixture" }},
		{"windows alias", func(i *ContextDeletionIntent) { i.Context = "NUL" }},
		{"windows drive", func(i *ContextDeletionIntent) { i.Context = "C:private-fixture" }},
		{"trailing dot", func(i *ContextDeletionIntent) { i.Context = "staging." }},
		{"wrong root", func(i *ContextDeletionIntent) { i.ConfigFile = filepath.Join(t.TempDir(), "config.yaml") }},
		{"invalid secret name", func(i *ContextDeletionIntent) { i.Secrets[0].Name = "../private-fixture" }},
		{"invalid environment name", func(i *ContextDeletionIntent) { i.EnvNames[0] = "private-fixture" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			intent := contextDeletionConfigFixture(t)
			test.change(intent)
			require.ErrorIs(t, WriteContextDeletionIntent(intent), ErrContextDeletionIntentInvalid)
			raw, err := json.Marshal(intent)
			require.NoError(t, err)
			path, err := GetConfigPath()
			require.NoError(t, err)
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
			require.NoError(
				t,
				os.WriteFile(
					filepath.Join(filepath.Dir(path), ContextDeletionIntentFile),
					raw,
					0o600,
				),
			)
			err = CheckPendingContextDeletion()
			require.ErrorIs(t, err, ErrContextDeletionIntentInvalid)
			require.NotContains(t, err.Error(), "private-fixture")
			_, err = LockConfigForContextDeletion(intent.Context)
			require.ErrorIs(t, err, ErrContextDeletionIntentInvalid)
		})
	}
}

func TestContextDeletionIntentRejectsTrailingAndMalformedJSONWithoutValues(t *testing.T) {
	for _, raw := range []string{
		`{"secret":"private-fixture"}`,
		`{"schemaVersion": "private-fixture"}`,
		`{} {"secret":"private-fixture"}`,
	} {
		t.Run(raw, func(t *testing.T) {
			intent := contextDeletionConfigFixture(t)
			require.NoError(t, os.MkdirAll(filepath.Dir(intent.ConfigFile), 0o700))
			path := filepath.Join(filepath.Dir(intent.ConfigFile), ContextDeletionIntentFile)
			require.NoError(t, os.WriteFile(path, []byte(raw), 0o600))
			err := CheckPendingContextDeletion()
			require.ErrorIs(t, err, ErrContextDeletionIntentInvalid)
			require.NotContains(t, err.Error(), "private-fixture")
		})
	}
}

func TestContextDeletionIntentConfigSymlinkAliasWithDifferentStoreRootRefusesRecovery(
	t *testing.T,
) {
	if runtime.GOOS == localContextDeletionTestWindows {
		t.Skip("symlink creation depends on Windows privileges")
	}
	intent := contextDeletionConfigFixture(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(intent.ConfigFile), 0o700))
	require.NoError(t, os.WriteFile(intent.ConfigFile, []byte("contexts: {}"), 0o600))
	require.NoError(t, WriteContextDeletionIntent(intent))
	alias := filepath.Join(t.TempDir(), "config-alias.yaml")
	require.NoError(t, os.Symlink(intent.ConfigFile, alias))
	t.Setenv(EnvConfig, alias)
	require.ErrorIs(t, CheckPendingContextDeletion(), ErrContextDeletionIntentInvalid)
	_, err := LockConfig()
	require.ErrorIs(t, err, ErrContextDeletionIntentInvalid)
	_, err = LockConfigForContextDeletion("staging")
	require.ErrorIs(t, err, ErrContextDeletionIntentInvalid)
}

func TestContextDeletionIntentEquivalentDirectorySymlinkAliasesShareFence(t *testing.T) {
	if runtime.GOOS == localContextDeletionTestWindows {
		t.Skip("symlink creation depends on Windows privileges")
	}
	intent := contextDeletionConfigFixture(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(intent.ConfigFile), 0o700))
	require.NoError(t, os.WriteFile(intent.ConfigFile, []byte("contexts: {}"), 0o600))
	require.NoError(t, WriteContextDeletionIntent(intent))
	alias := filepath.Join(t.TempDir(), "config-directory-alias")
	require.NoError(t, os.Symlink(filepath.Dir(intent.ConfigFile), alias))
	t.Setenv(EnvConfig, filepath.Join(alias, filepath.Base(intent.ConfigFile)))
	require.ErrorIs(t, CheckPendingContextDeletion(), ErrContextDeletionPending)
	_, err := LockConfig()
	require.ErrorIs(t, err, ErrContextDeletionPending)
	unlock, err := LockConfigForContextDeletion("staging")
	require.NoError(t, err)
	unlock()
}

func TestContextDeletionIntentWriteRejectsOversizedMetadataBeforeMutation(t *testing.T) {
	intent := contextDeletionConfigFixture(t)
	intent.EnvNames = []string{strings.Repeat("A", maxContextDeletionIntentSize)}
	require.ErrorIs(t, WriteContextDeletionIntent(intent), ErrContextDeletionIntentInvalid)
	path := filepath.Join(filepath.Dir(intent.ConfigFile), ContextDeletionIntentFile)
	_, err := os.Stat(path)
	require.True(t, os.IsNotExist(err))
	require.NoError(t, CheckPendingContextDeletion())
}

func TestContextDeletionIntentAcceptsOrdinaryPortableNames(t *testing.T) {
	intent := contextDeletionConfigFixture(t)
	intent.Context = "first-stage_1"
	require.NoError(t, WriteContextDeletionIntent(intent))
	require.ErrorIs(t, CheckPendingContextDeletion(), ErrContextDeletionPending)
}
