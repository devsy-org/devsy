package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/hash"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
)

const (
	assetsRepository = "devsy-org/devsy-provider-microsandbox"
	assetsVersion    = "v9.9.9"
	osLinux          = "linux"
	archAmd64        = "amd64"
	assetsBinaryName = "devsy-runtime-microsandbox-linux-amd64"
)

type assetsServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests map[string]int
	failures map[string]int
	status   int
	version  string
	goos     string
	goarch   string
	binary   []byte
	checksum string
}

func newAssetsServer(t *testing.T, binary []byte, checksum string) *assetsServer {
	t.Helper()
	server := &assetsServer{
		requests: map[string]int{},
		failures: map[string]int{},
		status:   http.StatusGatewayTimeout,
		version:  assetsVersion,
		goos:     osLinux,
		goarch:   archAmd64,
		binary:   binary,
		checksum: checksum,
	}
	server.Server = httptest.NewTLSServer(http.HandlerFunc(server.serve))
	t.Cleanup(server.Close)
	return server
}

func (s *assetsServer) manifest() string {
	return fmt.Sprintf(`name: microsandbox
version: %s
exec:
  command: "true"
agent:
  driver: external
  external:
    binary: DEVSY_RUNTIME_DRIVER
    args: [serve]
  binaries:
    DEVSY_RUNTIME_DRIVER:
      - os: %s
        arch: %s
        path: %s/%s/releases/download/%s/%s
        name: %s
        checksum: %s
`, s.version, s.goos, s.goarch, s.URL, assetsRepository, assetsVersion, assetsBinaryName, assetsBinaryName, s.checksum)
}

func (s *assetsServer) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests[r.URL.Path]++
	remaining := s.failures[r.URL.Path]
	if remaining > 0 {
		s.failures[r.URL.Path]--
	}
	s.mu.Unlock()
	if remaining > 0 {
		http.Error(w, "<html>failure</html>", s.status)
		return
	}
	prefix := "/" + assetsRepository + "/releases/download/" + assetsVersion + "/"
	switch r.URL.Path {
	case prefix + providerManifestName:
		_, _ = w.Write([]byte(s.manifest()))
	case prefix + assetsBinaryName:
		_, _ = w.Write(s.binary)
	default:
		http.NotFound(w, r)
	}
}

func (s *assetsServer) count(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests["/"+assetsRepository+"/releases/download/"+assetsVersion+"/"+name]
}

func (s *assetsServer) fail(name string, times, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures["/"+assetsRepository+"/releases/download/"+assetsVersion+"/"+name] = times
	s.status = status
}

func (s *assetsServer) assets(t *testing.T, delays *[]time.Duration) providerAssets {
	t.Helper()
	return providerAssets{
		baseURL:    s.URL,
		repository: assetsRepository,
		version:    assetsVersion,
		outputDir:  t.TempDir(),
		goos:       s.goos,
		goarch:     s.goarch,
		attempts:   4,
		baseDelay:  time.Second,
		client:     s.Client(),
		sleep: func(_ context.Context, delay time.Duration) error {
			*delays = append(*delays, delay)
			return nil
		},
	}
}

func binaryFixture() ([]byte, string) {
	binary := []byte("runtime-binary")
	sum := sha256.Sum256(binary)
	return binary, hex.EncodeToString(sum[:])
}

func wrongChecksum() string {
	sum := sha256.Sum256([]byte("other"))
	return hex.EncodeToString(sum[:])
}

func (s *assetsServer) cachedBinary(a providerAssets) string {
	return filepath.Join(
		a.outputDir, providerCacheDirName,
		hash.String(fmt.Sprintf(
			"%s/%s/releases/download/%s/%s",
			s.URL, assetsRepository, assetsVersion, assetsBinaryName,
		)),
	)
}

func TestPrepareProviderRetriesGatewayTimeouts(t *testing.T) {
	binary, checksum := binaryFixture()
	server := newAssetsServer(t, binary, checksum)
	server.fail(providerManifestName, 2, http.StatusGatewayTimeout)
	var delays []time.Duration
	assets := server.assets(t, &delays)

	require.NoError(t, assets.prepare(context.Background(), &bytes.Buffer{}))
	require.Equal(t, 3, server.count(providerManifestName))
	require.Equal(t, 1, server.count(assetsBinaryName))
	require.Equal(t, []time.Duration{time.Second, 2 * time.Second}, delays)
	got, err := os.ReadFile(server.cachedBinary(assets))
	require.NoError(t, err)
	require.Equal(t, binary, got)
}

func TestPrepareProviderRetriesBinaryServerErrors(t *testing.T) {
	binary, checksum := binaryFixture()
	server := newAssetsServer(t, binary, checksum)
	server.fail(assetsBinaryName, 3, http.StatusBadGateway)
	var delays []time.Duration
	assets := server.assets(t, &delays)

	require.NoError(t, assets.prepare(context.Background(), &bytes.Buffer{}))
	require.Equal(t, 4, server.count(assetsBinaryName))
}

func TestPrepareProviderStopsAfterAttemptBudget(t *testing.T) {
	binary, checksum := binaryFixture()
	server := newAssetsServer(t, binary, checksum)
	server.fail(assetsBinaryName, 100, http.StatusGatewayTimeout)
	var delays []time.Duration
	assets := server.assets(t, &delays)

	err := assets.prepare(context.Background(), &bytes.Buffer{})
	require.ErrorContains(t, err, "after 4 attempts")
	require.ErrorContains(t, err, "status code 504")
	require.Equal(t, 4, server.count(assetsBinaryName))
}

func TestPrepareProviderDoesNotRetryClientErrors(t *testing.T) {
	binary, checksum := binaryFixture()
	server := newAssetsServer(t, binary, checksum)
	server.fail(providerManifestName, 100, http.StatusNotFound)
	var delays []time.Duration
	assets := server.assets(t, &delays)

	err := assets.prepare(context.Background(), &bytes.Buffer{})
	require.ErrorContains(t, err, "status code 404")
	require.Equal(t, 1, server.count(providerManifestName))
	require.Empty(t, delays)
}

func TestPrepareProviderRejectsChecksumMismatchWithoutRetry(t *testing.T) {
	binary, _ := binaryFixture()
	server := newAssetsServer(t, binary, wrongChecksum())
	var delays []time.Duration
	assets := server.assets(t, &delays)

	err := assets.prepare(context.Background(), &bytes.Buffer{})
	require.ErrorContains(t, err, "checksum mismatch")
	require.Equal(t, 1, server.count(assetsBinaryName))
	require.NoFileExists(t, server.cachedBinary(assets))
}

func TestPrepareProviderRejectsVersionMismatch(t *testing.T) {
	binary, checksum := binaryFixture()
	server := newAssetsServer(t, binary, checksum)
	server.version = "v9.9.8"
	var delays []time.Duration
	assets := server.assets(t, &delays)

	err := assets.prepare(context.Background(), &bytes.Buffer{})
	require.ErrorContains(t, err, `provider manifest version "v9.9.8" does not match release`)
	require.Zero(t, server.count(assetsBinaryName))
	require.NoFileExists(t, filepath.Join(assets.outputDir, providerManifestName))
}

func TestPrepareProviderReusesPreparedAssets(t *testing.T) {
	binary, checksum := binaryFixture()
	server := newAssetsServer(t, binary, checksum)
	var delays []time.Duration
	assets := server.assets(t, &delays)
	require.NoError(t, assets.prepare(context.Background(), &bytes.Buffer{}))

	server.fail(providerManifestName, 100, http.StatusGatewayTimeout)
	server.fail(assetsBinaryName, 100, http.StatusGatewayTimeout)
	var out bytes.Buffer
	require.NoError(t, assets.prepare(context.Background(), &out))
	require.Equal(t, 1, server.count(providerManifestName))
	require.Equal(t, 1, server.count(assetsBinaryName))
	require.Contains(t, out.String(), "already prepared")
}

func TestPrepareProviderRefetchesCorruptedBinary(t *testing.T) {
	binary, checksum := binaryFixture()
	server := newAssetsServer(t, binary, checksum)
	var delays []time.Duration
	assets := server.assets(t, &delays)
	require.NoError(t, assets.prepare(context.Background(), &bytes.Buffer{}))
	require.NoError(t, os.WriteFile(server.cachedBinary(assets), []byte("corrupt"), 0o600))

	require.NoError(t, assets.prepare(context.Background(), &bytes.Buffer{}))
	require.Equal(t, 2, server.count(assetsBinaryName))
	got, err := os.ReadFile(server.cachedBinary(assets))
	require.NoError(t, err)
	require.Equal(t, binary, got)
}

func TestPrepareProviderCLIRequiresArguments(t *testing.T) {
	var stderr bytes.Buffer
	require.Equal(t, 2, prepareProviderCLI(nil, &bytes.Buffer{}, &stderr))
	require.Contains(t, stderr.String(), "--version and --output-dir")
}

func TestPrepareProviderDoesNotRetryLocalFileErrors(t *testing.T) {
	binary, checksum := binaryFixture()
	server := newAssetsServer(t, binary, checksum)
	var delays []time.Duration
	assets := server.assets(t, &delays)
	blocker := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(blocker, nil, 0o600))
	assets.outputDir = filepath.Join(blocker, "nested")

	require.Error(t, assets.prepare(context.Background(), &bytes.Buffer{}))
	require.Equal(t, 1, server.count(providerManifestName))
	require.Empty(t, delays)
}

func TestPrepareProviderRefetchesManifestFromAnotherRepository(t *testing.T) {
	binary, checksum := binaryFixture()
	server := newAssetsServer(t, binary, checksum)
	var delays []time.Duration
	assets := server.assets(t, &delays)
	other := fmt.Sprintf(
		"name: microsandbox\nversion: %s\nexec:\n  command: \"true\"\nagent:\n  driver: external\n"+
			"  external:\n    binary: DEVSY_RUNTIME_DRIVER\n  binaries:\n    DEVSY_RUNTIME_DRIVER:\n"+
			"      - os: linux\n        arch: amd64\n        checksum: %s\n"+
			"        path: https://github.com/other/repo/releases/download/%s/runtime\n",
		assetsVersion, checksum, assetsVersion,
	)
	require.NoError(
		t,
		os.WriteFile(filepath.Join(assets.outputDir, providerManifestName), []byte(other), 0o600),
	)

	require.NoError(t, assets.prepare(context.Background(), &bytes.Buffer{}))
	require.Equal(t, 1, server.count(providerManifestName))
	require.FileExists(t, server.cachedBinary(assets))
}

func TestPreparedCacheSatisfiesProviderDownloader(t *testing.T) {
	binary, checksum := binaryFixture()
	server := newAssetsServer(t, binary, checksum)
	var delays []time.Duration
	server.goos, server.goarch = runtime.GOOS, runtime.GOARCH
	assets := server.assets(t, &delays)
	config, err := assets.loadManifest(
		context.Background(),
		filepath.Join(assets.outputDir, providerManifestName),
	)
	require.NoError(t, err)
	require.NoError(t, assets.prepare(context.Background(), &bytes.Buffer{}))

	home := t.TempDir()
	cache := filepath.Join(home, "cache", "providers")
	require.NoError(t, os.MkdirAll(cache, 0o750))
	entries, err := os.ReadDir(filepath.Join(assets.outputDir, providerCacheDirName))
	require.NoError(t, err)
	for _, entry := range entries {
		data, err := os.ReadFile(
			filepath.Join(assets.outputDir, providerCacheDirName, entry.Name()),
		)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(cache, entry.Name()), data, 0o600))
	}
	t.Setenv("DEVSY_HOME", home)
	server.Close()

	paths, err := provider.DownloadBinaries(
		context.Background(),
		config.Agent.Binaries,
		t.TempDir(),
	)
	require.NoError(t, err)
	got, err := os.ReadFile(paths[runtimeDriverBinary])
	require.NoError(t, err)
	require.Equal(t, binary, got)
}

type diskFullWriter struct{}

func (diskFullWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestLocalWriterMarksWriteFailuresNonRetryable(t *testing.T) {
	_, err := io.Copy(localWriter{diskFullWriter{}}, strings.NewReader("data"))
	require.Error(t, err)
	require.False(t, retryableFetch(context.Background(), err))
	require.True(t, retryableFetch(context.Background(), io.ErrUnexpectedEOF))
}
