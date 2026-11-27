package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/internal/dependencytest"
	"github.com/stretchr/testify/require"
)

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
