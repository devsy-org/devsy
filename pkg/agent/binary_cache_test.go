package agent

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/require"
)

func TestHTTPDownloadSourceStreamsAndCaches(t *testing.T) {
	const payload = "agent binary contents"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, payload)
	}))
	defer server.Close()

	cache := &BinaryCache{BaseDir: t.TempDir()}
	source := &HTTPDownloadSource{BaseURL: server.URL, Cache: cache, Version: "v1"}
	binary, err := source.GetBinary(context.Background(), "amd64")
	require.NoError(t, err)
	defer func() { require.NoError(t, binary.Close()) }()

	data, err := io.ReadAll(binary)
	require.NoError(t, err)
	require.Equal(t, payload, string(data))

	cached, err := cache.Get("amd64")
	require.NoError(t, err)
	defer func() { require.NoError(t, cached.Close()) }()
	cachedData, err := io.ReadAll(cached)
	require.NoError(t, err)
	require.Equal(t, payload, string(cachedData))
	require.Equal(t, "v1", cache.ReadVersion("amd64"))
}

func TestHTTPDownloadSourceStreamsWithoutCacheDirectory(t *testing.T) {
	const payload = "agent binary contents"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, payload)
	}))
	defer server.Close()

	blockingPath := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(blockingPath, nil, 0o600))
	source := &HTTPDownloadSource{
		BaseURL: server.URL,
		Cache:   &BinaryCache{BaseDir: filepath.Join(blockingPath, "cache")},
	}
	binary, err := source.GetBinary(context.Background(), "amd64")
	require.NoError(t, err)
	defer func() { require.NoError(t, binary.Close()) }()

	data, err := io.ReadAll(binary)
	require.NoError(t, err)
	require.Equal(t, payload, string(data))
}

func TestHTTPDownloadSourceRemovesPartialCacheOnStreamFailure(t *testing.T) {
	streamErr := errors.New("stream failed")
	cache := &BinaryCache{BaseDir: t.TempDir()}
	source := &HTTPDownloadSource{Cache: cache, Version: "v1"}
	body := io.NopCloser(io.MultiReader(strings.NewReader("partial"), iotest.ErrReader(streamErr)))

	binary, err := source.cacheAndReturn("amd64", body)
	require.NoError(t, err)
	defer func() { require.NoError(t, binary.Close()) }()
	data, err := io.ReadAll(binary)
	require.ErrorIs(t, err, streamErr)
	require.Equal(t, "partial", string(data))

	_, err = cache.Get("amd64")
	require.ErrorIs(t, err, os.ErrNotExist)
	require.Empty(t, cache.ReadVersion("amd64"))
	entries, err := os.ReadDir(cache.BaseDir)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestHTTPDownloadSourceFallbackPreservesStreamError(t *testing.T) {
	blockingPath := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(blockingPath, nil, 0o600))
	source := &HTTPDownloadSource{
		Cache: &BinaryCache{BaseDir: filepath.Join(blockingPath, "cache")},
	}
	streamErr := errors.New("source disconnected")
	body := io.NopCloser(io.MultiReader(strings.NewReader("partial"), iotest.ErrReader(streamErr)))

	binary, err := source.cacheAndReturn("amd64", body)
	require.NoError(t, err)
	defer func() { require.NoError(t, binary.Close()) }()
	data, err := io.ReadAll(binary)
	require.ErrorIs(t, err, streamErr)
	require.Equal(t, "partial", string(data))
}
