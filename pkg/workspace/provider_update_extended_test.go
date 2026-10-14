package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	updateRuntimeKey         = "RUNTIME"
	updateRuntimeName        = "runtime"
	updateKeptOption         = "KEEP"
	updateObsoleteOption     = "OBSOLETE"
	updateNoopCommand        = "true"
	updateReplacementVersion = "2.0.0"
	updateFirstKey           = "FIRST"
	updateSecondKey          = "SECOND"
	updateWindowsOS          = "windows"
	updateSidecarPath        = "lib/sidecar"
	updateExecutableContent  = "new executable"
	updateUnrelatedContent   = "keep"
	updateRestoreFailure     = "restore failure"
	updateBackupPhase        = "backup"
	updatePromotionPhase     = "promotion"
	updateCurrentLink        = "current"
	updateMiddleLink         = "middle"
	updateEntryLink          = "entry"
)

type preservationFixture struct {
	t                   *testing.T
	config              *config.Config
	name, dir, binaries string
	before              map[string][]byte
	options             map[string]config.OptionValue
	workspace           *provider.Workspace
}

func newPreservationFixture(t *testing.T) *preservationFixture {
	t.Helper()
	setupTestPathManager(t)
	t.Setenv(config.EnvHome, t.TempDir())
	config.ResetPathManager()
	f := &preservationFixture{
		t:      t,
		name:   "update-preservation-extended",
		before: map[string][]byte{},
		options: map[string]config.OptionValue{
			updateKeptOption:     {Value: "kept", UserProvided: true},
			updateObsoleteOption: {Value: "retain on failure", UserProvided: true},
		},
	}
	f.config = &config.Config{
		DefaultContext: testDefaultContext,
		Contexts: map[string]*config.ContextConfig{testDefaultContext: {
			DefaultProvider: f.name,
			Providers: map[string]*config.ProviderConfig{
				f.name: {Initialized: true, InitAttempted: true, Options: f.options},
			},
		}},
	}
	require.NoError(t, config.SaveConfig(f.config))
	old := &provider.ProviderConfig{
		Name:    f.name,
		Version: "1.0.0",
		Exec:    provider.ProviderCommands{Command: []string{updateNoopCommand}},
		Source: provider.ProviderSource{
			Internal: true,
			Raw:      "microsandbox",
		},
		Options: map[string]*types.Option{updateKeptOption: {}, updateObsoleteOption: {}},
	}
	require.NoError(t, provider.SaveProviderConfig(testDefaultContext, old))
	var err error
	f.dir, err = provider.GetProviderDir(testDefaultContext, f.name)
	require.NoError(t, err)
	f.binaries, err = provider.GetProviderBinariesDir(testDefaultContext, f.name)
	require.NoError(t, err)
	path := filepath.Join(f.dir, provider.ProviderConfigFile)
	// #nosec G304 -- Path is the manifest in this fixture's isolated provider directory.
	f.before[path], err = os.ReadFile(path)
	require.NoError(t, err)
	f.put(filepath.Join(f.dir, "owned-sentinel"), []byte("untouched provider content"))
	f.put(filepath.Join(f.dir, "daemon", "owned-state"), []byte("untouched daemon state"))
	f.workspace = &provider.Workspace{
		ID:      "existing-workspace",
		UID:     "original-uid",
		Context: testDefaultContext,
		Provider: provider.WorkspaceProviderConfig{
			Name: f.name,
			Options: map[string]config.OptionValue{
				"WORKSPACE_OPTION": {Value: "original", UserProvided: true},
			},
		},
	}
	require.NoError(t, provider.SaveWorkspaceConfig(f.workspace))
	t.Logf("isolated provider root: %s", f.dir)
	return f
}

func (f *preservationFixture) put(path string, data []byte) {
	f.t.Helper()
	require.NoError(f.t, os.MkdirAll(filepath.Dir(path), 0o700))
	// #nosec G306 -- The fixture writes executable bytes to test provider binary preservation.
	require.NoError(f.t, os.WriteFile(path, data, 0o700))
	f.before[path] = append([]byte(nil), data...)
}

func (f *preservationFixture) binaryPath(key string) string {
	return filepath.Join(f.binaries, strings.ToLower(key), updateRuntimeName)
}

func (f *preservationFixture) seedBinary(key string, content []byte) {
	f.t.Helper()
	f.put(f.binaryPath(key), content)
	manifestPath := filepath.Join(f.dir, provider.ProviderConfigFile)
	// #nosec G304 -- Manifest path belongs to this fixture's isolated provider directory.
	raw, err := os.ReadFile(manifestPath)
	require.NoError(f.t, err)
	var old provider.ProviderConfig
	require.NoError(f.t, json.Unmarshal(raw, &old))
	if old.Binaries == nil {
		old.Binaries = map[string][]*provider.ProviderBinary{}
	}
	old.Binaries[key] = []*provider.ProviderBinary{
		updateTestBinary("unavailable-original-source/runtime", content),
	}
	require.NoError(f.t, provider.SaveProviderConfig(testDefaultContext, &old))
	// #nosec G304 -- Snapshot the manifest in this test-owned provider directory.
	f.before[manifestPath], err = os.ReadFile(manifestPath)
	require.NoError(f.t, err)
}

func updateTestDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func updateTestBinary(path string, content []byte) *provider.ProviderBinary {
	return &provider.ProviderBinary{
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
		Name:     updateRuntimeName,
		Path:     path,
		Checksum: updateTestDigest(content),
	}
}

func (f *preservationFixture) update(binaries map[string][]*provider.ProviderBinary) error {
	f.t.Helper()
	replacement := &provider.ProviderConfig{
		Name:     f.name,
		Version:  updateReplacementVersion,
		Exec:     provider.ProviderCommands{Command: []string{updateNoopCommand}},
		Options:  map[string]*types.Option{updateKeptOption: {}},
		Binaries: binaries,
	}
	raw, err := json.Marshal(replacement)
	require.NoError(f.t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = updateProvider(
		ctx,
		ProviderParams{
			DevsyConfig:  f.config,
			ProviderName: f.name,
			Raw:          raw,
			Source: &provider.ProviderSource{
				Raw: "owned-replacement",
				URL: "http://owned.invalid/provider.yaml",
			},
		},
	)
	require.NoError(f.t, ctx.Err(), "update must terminate by concrete result, not deadline")
	f.t.Logf("actual update result: %v", err)
	return err
}

func (f *preservationFixture) assertAssociation() *config.ProviderConfig {
	f.t.Helper()
	stored, err := config.LoadConfig(testDefaultContext, "")
	require.NoError(f.t, err)
	assert.Equal(f.t, f.name, stored.Current().DefaultProvider)
	workspace, err := provider.LoadWorkspaceConfig(testDefaultContext, f.workspace.ID)
	require.NoError(f.t, err)
	assert.Equal(f.t, f.workspace.UID, workspace.UID)
	assert.Equal(f.t, f.workspace.Provider, workspace.Provider)
	state := stored.Current().Providers[f.name]
	require.NotNil(f.t, state)
	f.t.Logf(
		"workspace and default association retained; initialized=%v options=%v",
		state.Initialized,
		state.Options,
	)
	return state
}

func (f *preservationFixture) assertFailedPreservation() {
	f.t.Helper()
	for path, expected := range f.before {
		// #nosec G304 -- Every snapshot path is created and owned by this fixture.
		actual, err := os.ReadFile(path)
		f.t.Logf("preserved path %s: exists=%v", path, err == nil)
		if assert.NoError(f.t, err, "existing bytes must survive: %s", path) {
			assert.Equal(f.t, expected, actual, path)
		}
	}
	state := f.assertAssociation()
	assert.True(f.t, state.Initialized)
	assert.True(f.t, state.InitAttempted)
	assert.Equal(f.t, f.options, state.Options)
}

func ownedProviderUpdateServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(func() {
		server.CloseClientConnections()
		server.Close()
		t.Log("owned HTTP server closed and joined")
	})
	return server
}

func TestProviderUpdateSameNameHTTPFailurePreservesExisting(t *testing.T) {
	f := newPreservationFixture(t)
	old := []byte("old same-name runtime")
	f.seedBinary(updateRuntimeKey, old)
	var mu sync.Mutex
	requests := 0
	oldPresentDuringRequest := false
	server := ownedProviderUpdateServer(t, func(w http.ResponseWriter, r *http.Request) {
		data, err := os.ReadFile(f.binaryPath(updateRuntimeKey))
		mu.Lock()
		requests++
		oldPresentDuringRequest = err == nil && string(data) == string(old)
		mu.Unlock()
		http.Error(w, "owned artifact unavailable", http.StatusNotFound)
	})
	err := f.update(
		map[string][]*provider.ProviderBinary{
			updateRuntimeKey: {
				updateTestBinary(server.URL+"/runtime", []byte("different checksum")),
			},
		},
	)
	require.ErrorContains(t, err, "received status code 404")
	mu.Lock()
	count, present := requests, oldPresentDuringRequest
	mu.Unlock()
	require.Equal(t, 1, count)
	t.Logf("old same-name bytes present during HTTP request: %v", present)
	assert.True(
		t,
		present,
		"live old binary must not be removed before a replacement download succeeds",
	)
	f.assertFailedPreservation()
}

func TestProviderUpdatePartialMultiBinaryFailurePreservesExisting(t *testing.T) {
	f := newPreservationFixture(t)
	for _, key := range []string{updateFirstKey, updateSecondKey} {
		f.seedBinary(key, []byte("old "+key))
	}
	replacement := []byte("new complete bytes unique to " + f.dir)
	var mu sync.Mutex
	requests := 0
	firstKey := ""
	firstPublishedBeforeFailure := false
	server := ownedProviderUpdateServer(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests++
		if requests == 1 {
			firstKey = strings.TrimPrefix(r.URL.Path, "/")
			_, _ = w.Write(replacement)
			return
		}
		data, err := os.ReadFile(f.binaryPath(firstKey))
		firstPublishedBeforeFailure = err == nil && string(data) == string(replacement)
		http.Error(w, "second requested artifact deliberately fails", http.StatusNotFound)
	})
	err := f.update(map[string][]*provider.ProviderBinary{
		updateFirstKey: {
			updateTestBinary(server.URL+"/FIRST", replacement),
		},
		updateSecondKey: {updateTestBinary(server.URL+"/SECOND", replacement)},
	})
	require.ErrorContains(t, err, "received status code 404")
	mu.Lock()
	count, first, published := requests, firstKey, firstPublishedBeforeFailure
	mu.Unlock()
	require.Equal(t, 2, count, "must complete first download before second request fails")
	require.Contains(t, []string{updateFirstKey, updateSecondKey}, first)
	t.Logf(
		"actual first completed artifact=%s; replacement bytes visible in live tree before second failure=%v",
		first,
		published,
	)
	assert.False(
		t,
		published,
		"a partial replacement must not publish any new live bytes before all preparation succeeds",
	)
	f.assertFailedPreservation()
}

func TestProviderUpdateInstalledOnlyOfflineReuse(t *testing.T) {
	f := newPreservationFixture(t)
	content := []byte("valid already installed offline executable")
	f.seedBinary(updateRuntimeKey, content)
	// A relative source that does not exist: only the valid installed target can satisfy this update.
	relative := filepath.Join(
		"definitely-absent-provider-test",
		filepath.Base(f.dir),
		"source-runtime",
	)
	_, statErr := os.Stat(relative)
	require.ErrorIs(t, statErr, os.ErrNotExist)
	err := f.update(
		map[string][]*provider.ProviderBinary{
			updateRuntimeKey: {updateTestBinary(relative, content)},
		},
	)
	require.NoError(t, err)
	actual, err := os.ReadFile(f.binaryPath(updateRuntimeKey))
	require.NoError(t, err)
	assert.Equal(t, content, actual)
	for _, path := range []string{filepath.Join(f.dir, "owned-sentinel"), filepath.Join(f.dir, "daemon", "owned-state")} {
		// #nosec G304 -- These are fixture-owned sentinel and daemon paths.
		actual, err = os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, f.before[path], actual)
	}
	// Read persisted JSON directly: internal-load compatibility overlays must not hide stored state.
	raw, err := os.ReadFile(filepath.Join(f.dir, provider.ProviderConfigFile))
	require.NoError(t, err)
	var replacement provider.ProviderConfig
	require.NoError(t, json.Unmarshal(raw, &replacement))
	assert.Equal(t, updateReplacementVersion, replacement.Version)
	assert.Equal(t, f.name, replacement.Name)
	assert.Equal(t, "owned-replacement", replacement.Source.Raw)
	state := f.assertAssociation()
	assert.False(t, state.Initialized)
	assert.True(t, state.InitAttempted)
	assert.Equal(
		t,
		map[string]config.OptionValue{updateKeptOption: f.options[updateKeptOption]},
		state.Options,
	)
	t.Log(
		"PASS: installed-only matching checksum reused without source file or network; " +
			"successful-update state semantics preserved",
	)
}

func TestProviderUpdateAbsoluteChecksumMismatchPreservesExternal(t *testing.T) {
	f := newPreservationFixture(t)
	external := filepath.Join(t.TempDir(), "externally-owned-runtime")
	content := []byte("external local executable owned independently")
	f.put(external, content)
	require.False(t, strings.HasPrefix(external, f.dir+string(filepath.Separator)))
	err := f.update(
		map[string][]*provider.ProviderBinary{
			updateRuntimeKey: {updateTestBinary(external, []byte("mismatched expected content"))},
		},
	)
	require.Error(t, err)
	require.ErrorContains(t, err, "download binaries")
	t.Logf(
		"absolute source is outside providerDir; observed error after mismatch: %s",
		fmt.Sprint(err),
	)
	f.assertFailedPreservation()
}

// Windows may require a privilege or Developer Mode to create symbolic links.
// Only tests that exercise links use this check; ordinary update tests still run.
func requireProviderUpdateSymlinks(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	file := filepath.Join(root, "file")
	require.NoError(t, os.WriteFile(file, []byte("owned link capability control"), 0o600))
	dir := filepath.Join(root, "directory")
	require.NoError(t, os.Mkdir(dir, 0o700))
	for _, target := range []string{file, dir} {
		err := os.Symlink(target, target+"-link")
		if err != nil && runtime.GOOS == updateWindowsOS {
			t.Skipf("symbolic links unavailable on this Windows runner: %v", err)
		}
		require.NoError(t, err)
	}
}
