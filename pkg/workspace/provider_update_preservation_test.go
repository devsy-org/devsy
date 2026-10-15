package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProviderUpdateFailedDownloadPreservesExisting(t *testing.T) {
	f := newFailedDownloadFixture(t)
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "owned replacement artifact deliberately unavailable", http.StatusNotFound)
	}))
	t.Cleanup(func() {
		server.CloseClientConnections()
		server.Close()
		t.Log(
			"owned HTTP server closed and handlers joined; t.TempDir cleanup removes isolated provider/config/workspace state",
		)
	})
	digest := sha256.Sum256([]byte(f.directory + " never served replacement"))
	replacement := &provider.ProviderConfig{
		Name: f.name, Version: updateReplacementVersion,
		Exec:    provider.ProviderCommands{Command: []string{updateNoopCommand}},
		Options: map[string]*types.Option{updateKeptOption: {}},
		Binaries: map[string][]*provider.ProviderBinary{"NEW_RUNTIME": {{
			OS: runtime.GOOS, Arch: runtime.GOARCH, Name: "new-runtime",
			Path: server.URL + "/runtime", Checksum: hex.EncodeToString(digest[:]),
		}}},
	}
	raw, err := json.Marshal(replacement)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = updateProvider(ctx, ProviderParams{
		DevsyConfig:  f.config,
		ProviderName: f.name,
		Raw:          raw,
		Source: &provider.ProviderSource{
			URL: server.URL + "/provider.yaml",
			Raw: server.URL + "/provider.yaml",
		},
	})
	require.ErrorContains(t, err, "download binaries")
	require.NoError(t, ctx.Err(), "failure must be artifact HTTP status, not deadline")
	require.EqualValues(t, 1, requests.Load())
	t.Logf("actual updateProvider failure: %v", err)
	f.assertPreserved(t)
}

func (f *failedDownloadFixture) assertPreserved(t *testing.T) {
	t.Helper()
	for path, before := range f.files {
		// #nosec G304 -- Snapshot paths are created only by the isolated preservation fixture.
		after, readErr := os.ReadFile(path)
		relative, relErr := filepath.Rel(f.directory, path)
		require.NoError(t, relErr)
		if assert.NoError(
			t,
			readErr,
			"existing artifact %s must survive failed replacement",
			relative,
		) {
			assert.Equal(t, before, after, "existing artifact %s bytes must survive", relative)
		}
		t.Logf("preservation artifact %s: present=%v", relative, readErr == nil)
	}
	stored, err := config.LoadConfig(testDefaultContext, "")
	require.NoError(t, err)
	state := stored.Current().Providers[f.name]
	require.NotNil(t, state)
	assert.True(t, state.Initialized, "failed replacement must preserve initialized state")
	assert.True(t, state.InitAttempted)
	assert.Equal(
		t,
		f.options,
		state.Options,
		"failed replacement must preserve all existing options",
	)
	assert.Equal(t, f.name, stored.Current().DefaultProvider)
	t.Logf(
		"persisted state: initialized=%v initAttempted=%v keep=%q obsolete=%q",
		state.Initialized,
		state.InitAttempted,
		state.Options[updateKeptOption].Value,
		state.Options[updateObsoleteOption].Value,
	)
	loadedWorkspace, err := provider.LoadWorkspaceConfig(testDefaultContext, f.workspace.ID)
	require.NoError(t, err)
	assert.Equal(t, f.workspace.UID, loadedWorkspace.UID)
	assert.Equal(t, f.workspace.Provider, loadedWorkspace.Provider)
	t.Logf("workspace association retained: id=%s uid=%s provider=%s", loadedWorkspace.ID,
		loadedWorkspace.UID, loadedWorkspace.Provider.Name)
}

type failedDownloadFixture struct {
	config          *config.Config
	name, directory string
	options         map[string]config.OptionValue
	files           map[string][]byte
	workspace       *provider.Workspace
}

func newFailedDownloadFixture(t *testing.T) *failedDownloadFixture {
	t.Helper()
	setupTestPathManager(t)
	t.Setenv(config.EnvHome, t.TempDir())
	config.ResetPathManager()
	const name = "update-preservation-test"
	oldOptions := map[string]config.OptionValue{
		updateKeptOption:     {Value: "old-kept-value", UserProvided: true},
		updateObsoleteOption: {Value: "old-obsolete-value", UserProvided: true},
	}
	initial := &config.Config{
		DefaultContext: testDefaultContext,
		Contexts: map[string]*config.ContextConfig{
			testDefaultContext: {
				DefaultProvider: name,
				Providers: map[string]*config.ProviderConfig{
					name: {Initialized: true, InitAttempted: true, Options: oldOptions},
				},
			},
		},
	}
	require.NoError(t, config.SaveConfig(initial))
	oldProvider := &provider.ProviderConfig{
		Name: name, Version: "1.0.0",
		Exec:    provider.ProviderCommands{Command: []string{updateNoopCommand}},
		Source:  provider.ProviderSource{Internal: true, Raw: "microsandbox"},
		Options: map[string]*types.Option{updateKeptOption: {}, updateObsoleteOption: {}},
	}
	require.NoError(t, provider.SaveProviderConfig(testDefaultContext, oldProvider))
	providerDir, err := provider.GetProviderDir(testDefaultContext, name)
	require.NoError(t, err)
	binariesDir, err := provider.GetProviderBinariesDir(testDefaultContext, name)
	require.NoError(t, err)
	oldBinary := filepath.Join(binariesDir, "old-runtime", "old-runtime")
	require.NoError(t, os.MkdirAll(filepath.Dir(oldBinary), 0o700))
	// #nosec G306 -- The fixture must preserve an executable, not only a non-executable data file.
	require.NoError(t, os.WriteFile(oldBinary, []byte("old executable bytes"), 0o700))
	sentinel := filepath.Join(providerDir, "owned-sentinel")
	require.NoError(t, os.WriteFile(sentinel, []byte("existing provider user data"), 0o600))
	oldFiles := map[string][]byte{}
	for _, path := range []string{filepath.Join(providerDir, provider.ProviderConfigFile), oldBinary, sentinel} {
		// #nosec G304 -- These manifest, binary and sentinel paths are test-owned fixture files.
		oldFiles[path], err = os.ReadFile(path)
		require.NoError(t, err)
	}
	workspace := &provider.Workspace{
		ID:      "preserved-ws",
		UID:     "original-uid",
		Context: testDefaultContext,
		Provider: provider.WorkspaceProviderConfig{
			Name: name,
			Options: map[string]config.OptionValue{
				"WORKSPACE_OPTION": {Value: "unchanged", UserProvided: true},
			},
		},
	}
	require.NoError(t, provider.SaveWorkspaceConfig(workspace))
	return &failedDownloadFixture{
		config: initial, name: name, directory: providerDir,
		options: oldOptions, files: oldFiles, workspace: workspace,
	}
}
