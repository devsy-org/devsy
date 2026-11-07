package provider

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/internal/dependencytest"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/devsy-org/devsy/providers"
	"github.com/stretchr/testify/require"
)

func TestLoadProviderConfigReadOnlyOverlay(t *testing.T) {
	for _, tt := range []struct {
		name, source string
		internal     bool
	}{
		{"custom-docker", DockerDriver, true},
		{"devsy-pro", "pro", true},
		{"custom-microsandbox", MicrosandboxDriver, true},
		{DockerDriver, "retired-source", true},
		{DockerDriver, DockerDriver, false},
	} {
		t.Run(tt.name+"-"+tt.source, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv(config.EnvHome, root)
			pm := config.NewReadOnlyPathManager()
			stored := &ProviderConfig{
				Name: tt.name, Version: "v0.0.8", Description: "custom description",
				Source: ProviderSource{Internal: tt.internal, Raw: tt.source},
				Options: map[string]*types.Option{
					"CUSTOM":  {Default: "retained"},
					"DYNAMIC": {Command: "must never execute"},
				},
				Exec: ProviderCommands{Command: []string{staleHelperShCommand}},
				Agent: ProviderAgentConfig{
					Path: "/custom/agent",
					Exec: ProviderAgentConfigExec{Shutdown: []string{staleHelperShCommand}},
				},
			}
			if !tt.internal {
				stored.Source.Github = "example/retained-provider"
			}
			payload, err := json.Marshal(stored)
			require.NoError(t, err)
			dir, err := pm.ProviderDir(config.DefaultContext, stored.Name)
			require.NoError(t, err)
			require.NoError(t, os.MkdirAll(dir, 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(dir, ProviderConfigFile), payload, 0o600))
			want, err := ParseProvider(strings.NewReader(string(payload)))
			require.NoError(t, err)
			if tt.internal && tt.source != "retired-source" {
				embedded, err := ParseProvider(
					strings.NewReader(providers.GetBuiltInProviders()[tt.source]),
				)
				require.NoError(t, err)
				want.Exec, want.Agent.Exec = embedded.Exec, embedded.Agent.Exec
				require.NotEqual(t, stored.Exec, want.Exec)
			}
			defaultManager := config.DefaultPathManager()
			dependencytest.Preserve(t, root)
			loaded, err := LoadProviderConfigWithPathManager(pm, config.DefaultContext, stored.Name)
			require.NoError(t, err)
			require.Equal(t, want, loaded)
			legacy, err := LoadProviderConfig(config.DefaultContext, stored.Name)
			require.NoError(t, err)
			require.Equal(t, legacy, loaded)
			require.Same(t, defaultManager, config.DefaultPathManager())
		})
	}
}

func TestLoadProviderConfigReadOnlyMissing(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "missing home")
	t.Setenv(config.EnvHome, home)
	defaultManager := config.DefaultPathManager()
	t.Run("read only", func(t *testing.T) {
		dependencytest.Preserve(t, root)
		loaded, err := LoadProviderConfigWithPathManager(
			config.NewReadOnlyPathManager(),
			"ctx",
			"missing",
		)
		require.Nil(t, loaded)
		require.ErrorIs(t, err, fs.ErrNotExist)
		var pathErr *os.PathError
		require.ErrorAs(t, err, &pathErr)
		require.Equal(
			t,
			filepath.Join(home, "contexts", "ctx", "providers", "missing", ProviderConfigFile),
			pathErr.Path,
		)
		require.Same(t, defaultManager, config.DefaultPathManager())
		require.NoDirExists(t, home)
	})
	_, err := LoadProviderConfig("ctx", "missing")
	require.ErrorIs(t, err, fs.ErrNotExist)
	require.DirExists(t, home)
	require.NoDirExists(t, filepath.Join(home, "contexts"))
}

func TestLoadProviderConfigReadOnlyInvalidFiles(t *testing.T) {
	for _, tt := range []struct {
		name, payload, message string
	}{
		{"missing", "", ""},
		{"malformed", "name: [", "parse provider config:"},
		{"invalid definition", `{ "name": "valid-name" }`, "validate:"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv(config.EnvHome, root)
			pm := config.NewReadOnlyPathManager()
			dir, err := pm.ProviderDir("ctx", DockerDriver)
			require.NoError(t, err)
			require.NoError(t, os.MkdirAll(dir, 0o700))
			if tt.payload != "" {
				require.NoError(
					t,
					os.WriteFile(filepath.Join(dir, ProviderConfigFile), []byte(tt.payload), 0o600),
				)
			}
			dependencytest.Preserve(t, root)
			loaded, err := LoadProviderConfigWithPathManager(pm, "ctx", DockerDriver)
			require.Nil(t, loaded)
			require.Error(t, err)
			_, legacyErr := LoadProviderConfig("ctx", DockerDriver)
			require.EqualError(t, err, legacyErr.Error())
			if tt.payload == "" {
				require.ErrorIs(t, err, fs.ErrNotExist)
			} else {
				require.ErrorContains(t, err, tt.message)
			}
		})
	}
}

type failingProviderPathManager struct {
	config.PathManager
	err error
}

func (pm failingProviderPathManager) ProviderDir(
	string,
	string,
) (string, error) {
	return "", pm.err
}

func TestLoadProviderConfigReadOnlyManagerFailure(t *testing.T) {
	root := t.TempDir()
	t.Setenv(config.EnvHome, filepath.Join(root, "must remain absent"))
	dependencytest.Preserve(t, root)
	wantErr := errors.New("provider path unavailable")
	loaded, err := LoadProviderConfigWithPathManager(
		failingProviderPathManager{err: wantErr},
		"ctx",
		DockerDriver,
	)
	require.Nil(t, loaded)
	require.ErrorIs(t, err, wantErr)
	require.Same(t, wantErr, err)
	loaded, err = LoadProviderConfigWithPathManager(nil, "ctx", DockerDriver)
	require.Nil(t, loaded)
	require.EqualError(t, err, "load provider config: path manager is required")
}
