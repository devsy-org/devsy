package provider

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/devsy-org/devsy/pkg/extract"
	"github.com/stretchr/testify/require"
)

func TestExtractConfinedUpdateTarExpandsGNUSparseExecutable(t *testing.T) {
	archive := gnuSparseUpdateArchive(t)
	wantExecutable := []byte("#!/bin\x00\x00\x00sh\n")
	wantSidecar := []byte("provider metadata\n")

	legacyDir := t.TempDir()
	require.NoError(t, extract.Extract(bytes.NewReader(archive), legacyDir))
	// #nosec G304 -- legacyDir and the archive are owned entirely by this fixture.
	legacyExecutable, err := os.ReadFile(filepath.Join(legacyDir, "bin", "runtime"))
	require.NoError(t, err)
	require.Equal(t, wantExecutable, legacyExecutable)

	destination := filepath.Join(t.TempDir(), "provider")
	require.NoError(t, os.Mkdir(destination, 0o700))
	root, err := os.OpenRoot(destination)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, root.Close()) })

	require.NoError(t, extractConfinedUpdateTar(
		context.Background(), root, bytes.NewReader(archive),
	))
	require.NoError(t, finalizeUpdateArchive(context.Background(), root, "bin/runtime"))

	executable, err := root.ReadFile("bin/runtime")
	require.NoError(t, err)
	require.Equal(t, wantExecutable, executable)
	executableInfo, err := root.Stat("bin/runtime")
	require.NoError(t, err)
	assertSparseUpdateFileMode(t, executableInfo, 0o755)

	sidecar, err := root.ReadFile("bin/provider.json")
	require.NoError(t, err)
	require.Equal(t, wantSidecar, sidecar)
	sidecarInfo, err := root.Stat("bin/provider.json")
	require.NoError(t, err)
	assertSparseUpdateFileMode(t, sidecarInfo, 0o640)
}

func assertSparseUpdateFileMode(t *testing.T, info os.FileInfo, unixMode os.FileMode) {
	t.Helper()
	require.True(t, info.Mode().IsRegular())
	if runtime.GOOS == windowsOS {
		// Windows reports writable regular files as 0666 instead of Unix permission bits.
		require.Equal(t, os.FileMode(0o666), info.Mode().Perm())
		return
	}
	require.Equal(t, unixMode, info.Mode().Perm())
}

// This fixture is a GNU sparse tar stream parsed by archive/tar.Reader. The
// regular-file header is patched to the GNU sparse type with two data extents.
func gnuSparseUpdateArchive(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := tar.NewWriter(&buf)
	physical := []byte("#!/binsh\n")
	require.NoError(t, writer.WriteHeader(&tar.Header{
		Name: "bin/runtime", Mode: 0o755, Size: int64(len(physical)), Format: tar.FormatGNU,
	}))
	_, err := writer.Write(physical)
	require.NoError(t, err)
	sidecar := []byte("provider metadata\n")
	require.NoError(t, writer.WriteHeader(&tar.Header{
		Name: "bin/provider.json", Mode: 0o640, Size: int64(len(sidecar)),
	}))
	_, err = writer.Write(sidecar)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	archive := buf.Bytes()
	header := archive[:512]
	header[156] = tar.TypeGNUSparse
	putSparseNumber(t, header[386:398], 0)
	putSparseNumber(t, header[398:410], 6)
	putSparseNumber(t, header[410:422], 9)
	putSparseNumber(t, header[422:434], 3)
	putSparseNumber(t, header[483:495], 12)
	for i := 148; i < 156; i++ {
		header[i] = ' '
	}
	var checksum int
	for _, b := range header {
		checksum += int(b)
	}
	copy(header[148:156], fmt.Sprintf("%06o\x00 ", checksum))
	return archive
}

func putSparseNumber(t *testing.T, field []byte, value int64) {
	t.Helper()
	text := fmt.Sprintf("%011o\x00", value)
	require.Len(t, text, len(field))
	copy(field, text)
}

func TestGNUArchiveFixtureHasExpandedSparseData(t *testing.T) {
	archive := gnuSparseUpdateArchive(t)
	reader := tar.NewReader(bytes.NewReader(archive))
	header, err := reader.Next()
	require.NoError(t, err)
	require.Equal(t, byte(tar.TypeGNUSparse), header.Typeflag)
	require.Equal(t, int64(12), header.Size)
	body, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, []byte("#!/bin\x00\x00\x00sh\n"), body)
	header, err = reader.Next()
	require.NoError(t, err)
	require.Equal(t, "bin/provider.json", header.Name)
}
