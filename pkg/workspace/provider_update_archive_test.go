package workspace

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
)

type ownedArchiveEntry struct{ name, target, body string }

func ownedContainmentArchive(t *testing.T, entries []ownedArchiveEntry) []byte {
	t.Helper()
	var data bytes.Buffer
	gz := gzip.NewWriter(&data)
	writer := tar.NewWriter(gz)
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Mode: 0o700, Size: int64(len(entry.body))}
		if entry.target != "" {
			header.Typeflag = tar.TypeSymlink
			header.Linkname = entry.target
			header.Size = 0
		}
		require.NoError(t, writer.WriteHeader(header))
		if entry.body != "" {
			_, err := writer.Write([]byte(entry.body))
			require.NoError(t, err)
		}
	}
	require.NoError(t, writer.Close())
	require.NoError(t, gz.Close())
	return data.Bytes()
}

func TestProviderUpdateArchivePreservesOwnedDaemon(t *testing.T) {
	requireProviderUpdateSymlinks(t)
	f := newPreservationFixture(t)
	f.seedBinary(updateRuntimeKey, []byte("old runtime"))
	entries := []ownedArchiveEntry{
		{name: "linkA", target: "."},
		{name: "linkB", target: "linkA/.."},
		{name: "linkC", target: "linkB/.."},
		{name: "linkD", target: "linkC/.."},
		{name: "linkD/daemon/owned-state", body: "archive escaped staging into owned daemon"},
		{name: updateRuntimeName, body: updateExecutableContent},
	}
	if runtime.GOOS == updateWindowsOS {
		// Windows cleans link targets before traversal, so use a direct relative
		// escape to the fixture-owned daemon rather than the POSIX link-order chain.
		entries = []ownedArchiveEntry{
			{name: "escape", target: filepath.Join("..", "..", "..", "daemon")},
			{name: "escape/owned-state", body: "archive escaped staging into owned daemon"},
			{name: updateRuntimeName, body: updateExecutableContent},
		}
	}
	archive := ownedContainmentArchive(t, entries)
	server := ownedProviderUpdateServer(
		t,
		func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(archive) },
	)
	binary := updateTestBinary(server.URL+"/owned.tar.gz", []byte(updateExecutableContent))
	binary.ArchivePath = updateRuntimeName
	err := f.update(map[string][]*provider.ProviderBinary{updateRuntimeKey: {binary}})
	require.Error(
		t,
		err,
		"confined preparation must reject the member before writing outside staging",
	)
	actual, readErr := os.ReadFile(filepath.Join(f.dir, "daemon", "owned-state"))
	require.NoError(t, readErr)
	t.Logf("owned daemon sentinel after preparation error=%q", actual)
	f.assertFailedPreservation()
	assertNoUpdateStaging(t, f)
}
