package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/internal/dependencytest"
	"github.com/stretchr/testify/require"
)

func TestLoadConfigReadOnlyMissing(t *testing.T) {
	for _, homeKind := range []string{"platform default", "absolute override", "relative override"} {
		t.Run(homeKind, func(t *testing.T) {
			root := t.TempDir()
			roots := setReadOnlyTestEnvironment(t, root)
			t.Chdir(root)
			wantOrigin := filepath.Join(roots.config, ConfigFile)
			if homeKind != "platform default" {
				home := filepath.Join(root, "missing home")
				if homeKind == "relative override" {
					home = "./missing home"
				}
				t.Setenv(EnvHome, home)
				wantOrigin = filepath.Join(home, ConfigFile)
			}
			defaultManager := DefaultPathManager()
			dependencytest.Preserve(t, root)
			for _, overrides := range [][2]string{{"", ""}, {"other", "custom"}} {
				loaded, err := LoadConfigWithPathManager(
					NewReadOnlyPathManager(),
					overrides[0],
					overrides[1],
				)
				require.NoError(t, err)
				wantContext := overrides[0]
				if wantContext == "" {
					wantContext = DefaultContext
				}
				require.Equal(t, &Config{
					SchemaVersion:  CurrentSchemaVersion,
					DefaultContext: wantContext,
					Origin:         wantOrigin,
					Contexts: map[string]*ContextConfig{wantContext: {
						DefaultProvider: overrides[1], Providers: map[string]*ProviderConfig{},
						IDEs: map[string]*IDEConfig{}, Options: map[string]OptionValue{},
					}},
				}, loaded)
			}
			require.Same(t, defaultManager, DefaultPathManager())
		})
	}
}

func TestLoadConfigReadOnlyNormalization(t *testing.T) {
	const changedProvider = "override-provider"
	for _, tt := range []struct {
		name, payload, context, provider, originalContext, originalProvider string
	}{
		{"empty", "{}", "", "", "", ""},
		{"stored defaults", "defaultContext: saved\ncontexts:\n  saved:\n    defaultProvider: old\n", "", "", "", ""},
		{
			"provider override", "defaultContext: saved\ncontexts:\n  saved:\n    defaultProvider: old\n",
			"", changedProvider, "", "old",
		},
		{
			"both overrides", "defaultContext: saved\ncontexts:\n  other:\n    defaultProvider: old\n",
			"other", changedProvider, "saved", "old",
		},
		{"unknown context", "defaultContext: saved\n", "new-context", changedProvider, "saved", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			setReadOnlyTestEnvironment(t, root)
			t.Setenv(EnvHome, root)
			path := filepath.Join(root, ConfigFile)
			require.NoError(t, os.WriteFile(path, []byte(tt.payload), 0o600))
			dependencytest.Preserve(t, root)

			loaded, err := LoadConfigWithPathManager(
				NewReadOnlyPathManager(),
				tt.context,
				tt.provider,
			)
			require.NoError(t, err)
			legacy, err := LoadConfig(tt.context, tt.provider)
			require.NoError(t, err)
			require.Equal(t, legacy, loaded)
			require.Equal(t, CurrentSchemaVersion, loaded.SchemaVersion)
			require.Equal(t, path, loaded.Origin)
			require.Equal(t, tt.originalContext, loaded.OriginalContext)
			require.Equal(t, tt.originalProvider, loaded.Current().OriginalProvider)
			require.Equal(t, tt.provider != "", loaded.Current().OriginalProviderSet)
			require.NotNil(t, loaded.Current().Options)
			require.NotNil(t, loaded.Current().Providers)
			require.NotNil(t, loaded.Current().IDEs)
			if tt.provider != "" {
				require.Equal(t, tt.provider, loaded.Current().DefaultProvider)
			}
		})
	}
}

func TestLoadConfigReadOnlyConfigOverride(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "existing"}[existing], func(t *testing.T) {
			root := t.TempDir()
			setReadOnlyTestEnvironment(t, root)
			home := filepath.Join(root, "unused home")
			t.Setenv(EnvHome, home)
			external := filepath.Join(root, "external config.yaml")
			t.Setenv(EnvConfig, external)
			if existing {
				require.NoError(
					t,
					os.WriteFile(
						external,
						[]byte("schemaVersion: 9\ndefaultContext: outside\n"),
						0o600,
					),
				)
			}
			dependencytest.Preserve(t, root)
			loaded, err := LoadConfigWithPathManager(NewReadOnlyPathManager(), "", "")
			require.NoError(t, err)
			legacy, err := LoadConfig("", "")
			require.NoError(t, err)
			require.Equal(t, legacy, loaded)
			require.Equal(t, external, loaded.Origin)
			if existing {
				require.Equal(t, "outside", loaded.DefaultContext)
				require.Equal(t, 9, loaded.SchemaVersion)
			}
			require.NoDirExists(t, home)
		})
	}
}

func TestLoadConfigReadOnlyErrors(t *testing.T) {
	const directoryFailure = "directory"
	for _, failure := range []string{"malformed", directoryFailure} {
		t.Run(failure, func(t *testing.T) {
			root := t.TempDir()
			setReadOnlyTestEnvironment(t, root)
			t.Setenv(EnvHome, root)
			path := filepath.Join(root, ConfigFile)
			if failure == directoryFailure {
				require.NoError(t, os.Mkdir(path, 0o700))
			} else {
				require.NoError(t, os.WriteFile(path, []byte("contexts: ["), 0o600))
			}
			dependencytest.Preserve(t, root)
			loaded, err := LoadConfigWithPathManager(NewReadOnlyPathManager(), "", "")
			require.Nil(t, loaded)
			require.Error(t, err)
			_, legacyErr := LoadConfig("", "")
			require.EqualError(t, err, legacyErr.Error())
			if failure == directoryFailure {
				var pathErr *os.PathError
				require.ErrorAs(t, err, &pathErr)
				require.Equal(t, path, pathErr.Path)
				require.ErrorContains(t, err, "read config:")
			}
		})
	}
}

type failingConfigPathManager struct {
	PathManager
	err error
}

func (pm failingConfigPathManager) ConfigFilePath() (string, error) { return "", pm.err }

func TestLoadConfigReadOnlyManagerFailure(t *testing.T) {
	root := t.TempDir()
	setReadOnlyTestEnvironment(t, root)
	t.Setenv(EnvHome, filepath.Join(root, "must remain absent"))
	dependencytest.Preserve(t, root)
	wantErr := errors.New("config path unavailable")
	loaded, err := LoadConfigWithPathManager(failingConfigPathManager{err: wantErr}, "", "")
	require.Nil(t, loaded)
	require.ErrorIs(t, err, wantErr)
	require.Same(t, wantErr, err)
	loaded, err = LoadConfigWithPathManager(nil, "", "")
	require.Nil(t, loaded)
	require.EqualError(t, err, "load config: path manager is required")
}

func TestLoadConfigReadOnlyPreservesDefaultManagerPolicy(t *testing.T) {
	root := t.TempDir()
	setReadOnlyTestEnvironment(t, root)
	home := filepath.Join(root, "normal home")
	t.Setenv(EnvHome, home)
	defaultManager := DefaultPathManager()
	t.Run("read only", func(t *testing.T) {
		dependencytest.Preserve(t, root)
		_, err := LoadConfigWithPathManager(NewReadOnlyPathManager(), "", "")
		require.NoError(t, err)
		require.Same(t, defaultManager, DefaultPathManager())
		_, err = os.Stat(home)
		require.ErrorIs(t, err, fs.ErrNotExist)
	})
	_, err := LoadConfig("", "")
	require.NoError(t, err)
	require.DirExists(t, home)
	require.NoFileExists(t, filepath.Join(home, ConfigFile))
}
