package jetbrains

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	devconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetDownloadURLs(t *testing.T) {
	const (
		latestAmd64 = "https://download.jetbrains.com/product?code=GO&platform=linux"
		latestArm64 = "https://download.jetbrains.com/product?code=GO&platform=linuxARM64"
		pinnedAmd64 = "https://download.jetbrains.com/go/goland-2025.2.tar.gz"
		pinnedArm64 = "https://download.jetbrains.com/go/goland-2025.2-aarch64.tar.gz"
		overrideURL = "https://override.test/goland.tar.gz"
	)

	tests := []struct {
		name      string
		values    map[string]config.OptionValue
		wantAmd64 string
		wantArm64 string
	}{
		{
			name:      "nil values select latest product URLs",
			values:    nil,
			wantAmd64: latestAmd64,
			wantArm64: latestArm64,
		},
		{
			name:      "empty version value falls back to latest",
			values:    map[string]config.OptionValue{VersionOption: {Value: ""}},
			wantAmd64: latestAmd64,
			wantArm64: latestArm64,
		},
		{
			name: "pinned version fills both architecture templates",
			values: map[string]config.OptionValue{
				VersionOption: {Value: "2025.2"},
			},
			wantAmd64: pinnedAmd64,
			wantArm64: pinnedArm64,
		},
		{
			name: "amd64 override replaces only the amd64 URL",
			values: map[string]config.OptionValue{
				VersionOption:       {Value: "2025.2"},
				DownloadAmd64Option: {Value: overrideURL},
			},
			wantAmd64: overrideURL,
			wantArm64: pinnedArm64,
		},
		{
			name: "empty overrides fall back to pinned templates",
			values: map[string]config.OptionValue{
				VersionOption:       {Value: "2025.2"},
				DownloadAmd64Option: {Value: ""},
				DownloadArm64Option: {Value: ""},
			},
			wantAmd64: pinnedAmd64,
			wantArm64: pinnedArm64,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			amd64, arm64 := getDownloadURLs(
				GolandOptions,
				tt.values,
				GolandProductCode,
				GolandDownloadAmd64Template,
				GolandDownloadArm64Template,
			)
			assert.Equal(t, tt.wantAmd64, amd64)
			assert.Equal(t, tt.wantArm64, arm64)
		})
	}
}

func TestGetDirectoryUnderHome(t *testing.T) {
	s := newGenericServer("alice", &GenericOptions{ID: "goland"})

	assert.Equal(
		t,
		"/home/alice/.cache/JetBrains/RemoteDev/dist/goland",
		s.getDirectory("/home/alice"),
	)
}

type tarEntry struct {
	name string
	body string
}

func buildTarGz(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name: e.name,
			Mode: 0o644,
			Size: int64(len(e.body)),
		}))
		_, err := tw.Write([]byte(e.body))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

func writeArchive(t *testing.T, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "goland.tar.gz")
	require.NoError(t, os.WriteFile(p, data, 0o600))
	return p
}

func TestExtractArchiveStripsTopLevelDirectory(t *testing.T) {
	archive := writeArchive(t, buildTarGz(t,
		tarEntry{name: "goland-2025.2/bin/goland.sh", body: "#!/bin/sh\n"},
		tarEntry{name: "goland-2025.2/product-info.json", body: "{}"},
	))
	dest := t.TempDir()
	s := newGenericServer("alice", &GenericOptions{ID: "goland"})

	require.NoError(t, s.extractArchive(archive, dest))

	got, err := os.ReadFile(filepath.Join(dest, "bin", "goland.sh"))
	require.NoError(t, err)
	assert.Equal(t, "#!/bin/sh\n", string(got))
	assert.FileExists(t, filepath.Join(dest, "product-info.json"))
	assert.NoDirExists(t, filepath.Join(dest, "goland-2025.2"))
}

func TestExtractArchiveErrors(t *testing.T) {
	s := newGenericServer("alice", &GenericOptions{ID: "goland"})

	t.Run("missing archive", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "absent.tar.gz")
		err := s.extractArchive(missing, t.TempDir())
		assert.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("not an archive", func(t *testing.T) {
		archive := writeArchive(t, []byte("this is not a tarball"))
		err := s.extractArchive(archive, t.TempDir())
		assert.Error(t, err)
	})

	t.Run("entry escaping destination after strip is rejected", func(t *testing.T) {
		base := t.TempDir()
		dest := filepath.Join(base, "install")
		archive := writeArchive(t, buildTarGz(t,
			tarEntry{name: "goland-2025.2/../../escape.sh", body: "owned"},
		))

		err := s.extractArchive(archive, dest)

		assert.ErrorContains(t, err, "path traversal")
		assert.NoFileExists(t, filepath.Join(base, "escape.sh"))
	})
}

func TestDownloadSelectsArchitectureURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("body:" + r.URL.Path))
	}))
	defer srv.Close()

	s := newGenericServer("alice", &GenericOptions{
		ID:            "goland",
		DisplayName:   "Goland",
		DownloadAmd64: srv.URL + "/amd64.tar.gz",
		DownloadArm64: srv.URL + "/arm64.tar.gz",
	})
	targetFolder := filepath.Join(t.TempDir(), "nested", "downloads")

	got, err := s.download(targetFolder)
	require.NoError(t, err)

	wantPath := "/amd64.tar.gz"
	if runtime.GOARCH == "arm64" {
		wantPath = "/arm64.tar.gz"
	}
	assert.Equal(t, filepath.Join(targetFolder, "goland.tar.gz"), filepath.Clean(got))
	body, err := os.ReadFile(got)
	require.NoError(t, err)
	assert.Equal(t, "body:"+wantPath, string(body))
}

func TestDownloadHTTPErrorLeavesNoArchive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	targetFolder := t.TempDir()
	s := newGenericServer("alice", &GenericOptions{
		ID:            "goland",
		DisplayName:   "Goland",
		DownloadAmd64: srv.URL + "/missing.tar.gz",
		DownloadArm64: srv.URL + "/missing.tar.gz",
	})

	_, err := s.download(targetFolder)

	assert.ErrorContains(t, err, "404")
	assert.NoFileExists(t, filepath.Join(targetFolder, "goland.tar.gz"))
}

func TestInstallPluginsSkipsWhenNoneConfigured(t *testing.T) {
	s := newGenericServer("alice", &GenericOptions{ID: "goland"})
	setupInfo := &devconfig.Result{MergedConfig: &devconfig.MergedDevContainerConfig{}}

	// The install directory has no bin/remote-dev-server.sh; any attempt to
	// run the installer would fail, so nil proves it was never invoked.
	err := s.installPlugins(setupInfo, filepath.Join(t.TempDir(), "missing"), t.TempDir())

	assert.NoError(t, err)
}
