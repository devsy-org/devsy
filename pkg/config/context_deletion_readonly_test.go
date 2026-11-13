package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/internal/dependencytest"
	"github.com/stretchr/testify/require"
)

const (
	deletionReadOnlyPrivateFixture = "readonly-private-fixture"
	deletionReadOnlyDirectoryAlias = "directory alias"
)

type deletionReadOnlyTestManager struct {
	PathManager
	configFile, data   string
	configErr, dataErr error
}

func (pm *deletionReadOnlyTestManager) ConfigFilePath() (string, error) {
	return pm.configFile, pm.configErr
}

func (pm *deletionReadOnlyTestManager) DataDir() (string, error) {
	return pm.data, pm.dataErr
}

func deletionReadOnlyFixture(
	t *testing.T,
) (string, *deletionReadOnlyTestManager, *ContextDeletionIntent) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	setReadOnlyTestEnvironment(t, root)
	ambient := filepath.Join(root, "ambient")
	t.Setenv(EnvHome, ambient)
	t.Setenv(EnvConfig, filepath.Join(ambient, ConfigFile))
	decoy := deletionReadOnlyIntent(filepath.Join(ambient, ConfigFile), ambient)
	decoy.Context = "ambient"
	writeDeletionReadOnlyFixture(t, decoy)
	pm := &deletionReadOnlyTestManager{
		PathManager: NewReadOnlyPathManager(),
		configFile:  filepath.Join(root, "explicit config", ConfigFile),
		data:        filepath.Join(root, "explicit data"),
	}
	intent := deletionReadOnlyIntent(pm.configFile, pm.data)
	writeDeletionReadOnlyFixture(t, intent)
	return root, pm, intent
}

func deletionReadOnlyIntent(configFile, data string) *ContextDeletionIntent {
	return &ContextDeletionIntent{
		SchemaVersion: 1, ConfigFile: configFile,
		StoreDirectory: filepath.Dir(configFile), DataDirectory: data,
		Context: "readonly-pending", Fingerprint: strings.Repeat("a", 64),
		Secrets:  []ContextDeletionSecret{{Name: "TOKEN", Backend: "file"}},
		EnvNames: []string{"PLAIN"},
	}
}

func writeDeletionReadOnlyFixture(t *testing.T, intent *ContextDeletionIntent) {
	t.Helper()
	require.NoError(t, os.MkdirAll(intent.DataDirectory, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Dir(intent.ConfigFile), 0o700))
	require.NoError(t, os.WriteFile(intent.ConfigFile, []byte("contexts: {}"), 0o600))
	raw, err := json.Marshal(intent)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		filepath.Join(filepath.Dir(intent.ConfigFile), ContextDeletionIntentFile), raw, 0o600,
	))
}

func preserveDeletionReadOnlyState(t *testing.T, root string) {
	t.Helper()
	pm := DefaultPathManager()
	dependencytest.Preserve(t, root)
	t.Cleanup(func() { require.Same(t, pm, DefaultPathManager()) })
}

func requireDeletionReadOnlyPending(t *testing.T, pm PathManager, context string) {
	t.Helper()
	err := CheckPendingContextDeletionWithPathManager(pm)
	require.ErrorIs(t, err, ErrContextDeletionPending)
	var pending *ContextDeletionPendingError
	require.ErrorAs(t, err, &pending)
	require.Equal(t, context, pending.Context)
	require.Equal(t, (&ContextDeletionPendingError{Context: context}).Error(), err.Error())
}

func TestReadOnlyDeletionFenceAbsentRoots(t *testing.T) {
	for _, state := range []string{"missing home", "missing config", "missing intent"} {
		t.Run(state, func(t *testing.T) {
			root := t.TempDir()
			setReadOnlyTestEnvironment(t, root)
			home := filepath.Join(root, "devsy")
			t.Setenv(EnvHome, home)
			if state != "missing home" {
				require.NoError(t, os.MkdirAll(home, 0o700))
			}
			if state == "missing intent" {
				require.NoError(
					t,
					os.WriteFile(filepath.Join(home, ConfigFile), []byte("{}"), 0o600),
				)
			}
			preserveDeletionReadOnlyState(t, root)
			require.NoError(t, CheckPendingContextDeletionWithPathManager(NewReadOnlyPathManager()))
		})
	}
}

func TestReadOnlyDeletionFenceConfigOutsideHome(t *testing.T) {
	root, _, intent := deletionReadOnlyFixture(t)
	t.Setenv(EnvHome, intent.DataDirectory)
	t.Setenv(EnvConfig, intent.ConfigFile)
	pm := NewReadOnlyPathManager()
	preserveDeletionReadOnlyState(t, root)
	requireDeletionReadOnlyPending(t, pm, intent.Context)
}

func TestReadOnlyDeletionFenceExplicitManagerIgnoresAmbientRoots(t *testing.T) {
	root, pm, intent := deletionReadOnlyFixture(t)
	preserveDeletionReadOnlyState(t, root)
	requireDeletionReadOnlyPending(t, pm, intent.Context)
}

func TestReadOnlyDeletionFenceRejectsInvalidMetadata(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*ContextDeletionIntent)
	}{
		{"future schema", func(i *ContextDeletionIntent) { i.SchemaVersion = 2 }},
		{"ambient config", func(i *ContextDeletionIntent) { i.ConfigFile = os.Getenv(EnvConfig) }},
		{"ambient store", func(i *ContextDeletionIntent) { i.StoreDirectory = os.Getenv(EnvHome) }},
		{"ambient data", func(i *ContextDeletionIntent) { i.DataDirectory = os.Getenv(EnvHome) }},
		{"unsafe context", func(i *ContextDeletionIntent) { i.Context = "../readonly-private" }},
		{"fingerprint", func(i *ContextDeletionIntent) { i.Fingerprint = deletionReadOnlyPrivateFixture }},
		{"secret name", func(i *ContextDeletionIntent) { i.Secrets[0].Name = deletionReadOnlyPrivateFixture }},
		{"secret backend", func(i *ContextDeletionIntent) { i.Secrets[0].Backend = deletionReadOnlyPrivateFixture }},
		{"environment name", func(i *ContextDeletionIntent) { i.EnvNames[0] = deletionReadOnlyPrivateFixture }},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, pm, intent := deletionReadOnlyFixture(t)
			test.change(intent)
			raw, err := json.Marshal(intent)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(
				filepath.Join(filepath.Dir(pm.configFile), ContextDeletionIntentFile), raw, 0o600,
			))
			preserveDeletionReadOnlyState(t, root)
			err = CheckPendingContextDeletionWithPathManager(pm)
			require.ErrorIs(t, err, ErrContextDeletionIntentInvalid)
			require.EqualError(t, err, ErrContextDeletionIntentInvalid.Error())
		})
	}
}

func TestReadOnlyDeletionFenceRejectsInvalidJSON(t *testing.T) {
	for _, kind := range []string{"corrupt", "trailing", "unknown", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			root, pm, intent := deletionReadOnlyFixture(t)
			raw, err := json.Marshal(intent)
			require.NoError(t, err)
			switch kind {
			case "corrupt":
				raw = []byte(`{"redacted":`)
			case "trailing":
				raw = append(raw, []byte(` {"redacted":true}`)...)
			case "unknown":
				raw = append([]byte(`{"redacted":true,`), raw[1:]...)
			case "oversize":
				raw = []byte(strings.Repeat(" ", maxContextDeletionIntentSize+1))
			}
			require.NoError(t, os.WriteFile(
				filepath.Join(filepath.Dir(pm.configFile), ContextDeletionIntentFile), raw, 0o600,
			))
			preserveDeletionReadOnlyState(t, root)
			err = CheckPendingContextDeletionWithPathManager(pm)
			require.ErrorIs(t, err, ErrContextDeletionIntentInvalid)
			require.EqualError(t, err, ErrContextDeletionIntentInvalid.Error())
		})
	}
}

func TestReadOnlyDeletionFenceRejectsNonregularIntent(t *testing.T) {
	for _, kind := range []string{"intent directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			if kind == "symlink" && runtime.GOOS == localContextDeletionTestWindows {
				t.Skip("symlink creation depends on Windows privileges")
			}
			root, pm, _ := deletionReadOnlyFixture(t)
			path := filepath.Join(filepath.Dir(pm.configFile), ContextDeletionIntentFile)
			target := filepath.Join(root, "preserved intent")
			require.NoError(t, os.Rename(path, target))
			if kind == "intent directory" {
				require.NoError(t, os.Mkdir(path, 0o700))
			} else {
				require.NoError(t, os.Symlink(target, path))
			}
			preserveDeletionReadOnlyState(t, root)
			require.ErrorIs(
				t,
				CheckPendingContextDeletionWithPathManager(pm),
				ErrContextDeletionIntentInvalid,
			)
		})
	}
}

func TestReadOnlyDeletionFenceConfigAliases(t *testing.T) {
	if runtime.GOOS == localContextDeletionTestWindows {
		t.Skip("symlink creation depends on Windows privileges")
	}
	for _, kind := range []string{"file alias", deletionReadOnlyDirectoryAlias, "dangling"} {
		t.Run(kind, func(t *testing.T) {
			root, pm, intent := deletionReadOnlyFixture(t)
			alias := filepath.Join(root, "alias")
			switch kind {
			case "file alias":
				require.NoError(t, os.Symlink(pm.configFile, alias))
				pm.configFile = alias
			case deletionReadOnlyDirectoryAlias:
				require.NoError(t, os.Symlink(filepath.Dir(pm.configFile), alias))
				pm.configFile = filepath.Join(alias, ConfigFile)
			case "dangling":
				require.NoError(t, os.Symlink("missing.yaml", alias))
				pm.configFile = alias
			}
			preserveDeletionReadOnlyState(t, root)
			if kind == deletionReadOnlyDirectoryAlias {
				requireDeletionReadOnlyPending(t, pm, intent.Context)
			} else {
				require.ErrorIs(
					t,
					CheckPendingContextDeletionWithPathManager(pm),
					ErrContextDeletionIntentInvalid,
				)
			}
		})
	}
}

func TestReadOnlyDeletionFencePathErrorsFailClosed(t *testing.T) {
	for _, kind := range []string{"nil", "config", "data", "missing data"} {
		t.Run(kind, func(t *testing.T) {
			root, pm, _ := deletionReadOnlyFixture(t)
			var supplied PathManager = pm
			switch kind {
			case "nil":
				supplied = nil
			case "config":
				pm.configErr = errors.New(deletionReadOnlyPrivateFixture)
			case "data":
				pm.dataErr = errors.New(deletionReadOnlyPrivateFixture)
			case "missing data":
				require.NoError(t, os.Remove(pm.data))
			}
			preserveDeletionReadOnlyState(t, root)
			err := CheckPendingContextDeletionWithPathManager(supplied)
			require.ErrorIs(t, err, ErrContextDeletionIntentInvalid)
			require.EqualError(t, err, ErrContextDeletionIntentInvalid.Error())
		})
	}
}
