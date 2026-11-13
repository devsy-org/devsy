package config

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/devsy-org/devsy/internal/dependencytest"
	"github.com/stretchr/testify/require"
)

func TestReadOnlyPathManagerExplicitHome(t *testing.T) {
	root := t.TempDir()
	defaults := setReadOnlyTestEnvironment(t, root)
	ambient := filepath.Join(root, "ambient home")
	t.Setenv(EnvHome, ambient)
	t.Chdir(root)
	for _, home := range []string{
		filepath.Join(root, "explicit home"),
		root + string(filepath.Separator) + "unused/../lexical home/.",
		"./relative home/../\u914d\u7f6e/",
		"  spaced home  ",
	} {
		t.Run(home, func(t *testing.T) {
			dependencytest.Preserve(t, root)
			assertReadOnlyPaths(
				t,
				NewReadOnlyPathManagerForHome(home),
				explicitHomeTestRoots(home, defaults.systemBin),
			)
			require.NoDirExists(t, home)
			require.NoDirExists(t, ambient)
		})
	}
}

func TestReadOnlyPathManagerExplicitHomeFileParent(t *testing.T) {
	root := t.TempDir()
	defaults := setReadOnlyTestEnvironment(t, root)
	t.Setenv(EnvHome, filepath.Join(root, "ambient"))
	home := filepath.Join(root, "file parent")
	require.NoError(t, os.WriteFile(home, []byte("unchanged"), 0o600))
	dependencytest.Preserve(t, root)

	assertReadOnlyPaths(
		t,
		NewReadOnlyPathManagerForHome(home),
		explicitHomeTestRoots(home, defaults.systemBin),
	)
}

func TestReadOnlyPathManagerExplicitEmptyHome(t *testing.T) {
	for _, ambientOverride := range []bool{false, true} {
		t.Run(
			map[bool]string{false: "native defaults", true: "ambient home"}[ambientOverride],
			func(t *testing.T) {
				root := t.TempDir()
				want := setReadOnlyTestEnvironment(t, root)
				if ambientOverride {
					home := filepath.Join(root, "ambient")
					t.Setenv(EnvHome, home)
					want = explicitHomeTestRoots(home, want.systemBin)
				}
				dependencytest.Preserve(t, root)

				assertReadOnlyPaths(t, NewReadOnlyPathManagerForHome(""), want)
				assertReadOnlyPaths(t, NewReadOnlyPathManager(), want)
			},
		)
	}
}

func TestLoadConfigWithExplicitHome(t *testing.T) {
	const externalConfig = "outside config.yaml"
	for _, tt := range []struct {
		name, override string
		existing       bool
	}{
		{name: "missing home config"},
		{name: "existing home config", existing: true},
		{name: "missing external config", override: externalConfig},
		{name: "existing external config", override: externalConfig, existing: true},
		{name: "relative external config", override: "./unused/../\u914d\u7f6e.yaml"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			setReadOnlyTestEnvironment(t, root)
			t.Chdir(root)
			ambient, home := filepath.Join(root, "ambient"), filepath.Join(root, "explicit")
			t.Setenv(EnvHome, ambient)
			require.NoError(t, os.MkdirAll(ambient, 0o700))
			ambientConfig := filepath.Join(ambient, ConfigFile)
			require.NoError(
				t,
				os.WriteFile(ambientConfig, []byte("defaultContext: decoy\n"), 0o600),
			)
			wantPath := filepath.Join(home, ConfigFile)
			if tt.override != "" {
				wantPath = tt.override
				if tt.override == externalConfig {
					wantPath = filepath.Join(root, tt.override)
				}
				t.Setenv(EnvConfig, wantPath)
			}
			if tt.existing {
				require.NoError(t, os.MkdirAll(filepath.Dir(wantPath), 0o700))
				payload := []byte("schemaVersion: 1\ndefaultContext: selected\n")
				require.NoError(t, os.WriteFile(wantPath, payload, 0o600))
			}
			defaultManager := DefaultPathManager()
			dependencytest.Preserve(t, root)
			pm := NewReadOnlyPathManagerForHome(home)
			path, err := pm.ConfigFilePath()
			require.NoError(t, err)
			require.Equal(t, wantPath, path)
			loaded, err := LoadConfigWithPathManager(pm, "", "")
			require.NoError(t, err)
			require.Equal(t, wantPath, loaded.Origin)
			require.Equal(t, CurrentSchemaVersion, loaded.SchemaVersion)
			wantContext := DefaultContext
			if tt.existing {
				wantContext = "selected"
			}
			require.Equal(t, wantContext, loaded.DefaultContext)
			require.Same(t, defaultManager, DefaultPathManager())
		})
	}
}

func TestExplicitHomePathManagerInstanceIsolation(t *testing.T) {
	root := t.TempDir()
	defaults := setReadOnlyTestEnvironment(t, root)
	firstHome, secondHome := filepath.Join(root, "first"), filepath.Join(root, "second")
	first := NewReadOnlyPathManagerForHome(firstHome)
	second := NewReadOnlyPathManagerForHome(secondHome)
	ordinary, readOnly := NewPathManager(), NewReadOnlyPathManager()
	empty := NewReadOnlyPathManagerForHome("")
	defaultManager := DefaultPathManager()
	for _, name := range []string{"ambient one", "ambient two"} {
		t.Run(name, func(t *testing.T) {
			ambient := filepath.Join(root, name)
			t.Setenv(EnvHome, ambient)
			ambientRoots := explicitHomeTestRoots(ambient, defaults.systemBin)
			assertReadOnlyPaths(t, ordinary, ambientRoots)
			dependencytest.Preserve(t, root)

			for range 2 {
				assertReadOnlyPaths(t, first, explicitHomeTestRoots(firstHome, defaults.systemBin))
				assertReadOnlyPaths(t, readOnly, ambientRoots)
				assertReadOnlyPaths(
					t,
					second,
					explicitHomeTestRoots(secondHome, defaults.systemBin),
				)
				assertReadOnlyPaths(t, empty, ambientRoots)
				assertReadOnlyPaths(t, defaultManager, ambientRoots)
			}
			require.Same(t, defaultManager, DefaultPathManager())
		})
	}
}

func TestExplicitHomePathManagerConcurrentIsolation(t *testing.T) {
	root := t.TempDir()
	dependencytest.Preserve(t, root)
	var workers sync.WaitGroup
	for _, name := range []string{"first", "second"} {
		home := filepath.Join(root, name)
		pm := NewReadOnlyPathManagerForHome(home)
		roots := explicitHomeTestRoots(home, "")
		paths := append(readOnlyCategoryCases(pm, roots), readOnlyDataCases(pm, roots.data)...)
		paths = append(paths, readOnlyOtherCases(pm, roots)...)
		for i := range paths {
			if paths[i].name == "ConfigFilePath" && os.Getenv(EnvConfig) != "" {
				paths[i].want = os.Getenv(EnvConfig)
			}
		}
		for range 10 {
			workers.Go(func() { checkConcurrentHomePaths(t, paths) })
		}
	}
	workers.Wait()
}

func explicitHomeTestRoots(home, systemBin string) readOnlyTestRoots {
	return readOnlyTestRoots{
		config: filepath.Clean(home), data: filepath.Clean(home),
		cache: filepath.Join(home, "cache"), state: filepath.Join(home, "state"),
		runtime: filepath.Join(home, "run"), systemBin: systemBin,
	}
}

func checkConcurrentHomePaths(t *testing.T, paths []readOnlyPathCase) {
	t.Helper()
	for _, path := range paths {
		got, err := path.fn()
		if err != nil || got != path.want {
			t.Errorf("%s() = %q, %v; want %q", path.name, got, err, path.want)
		}
	}
}
