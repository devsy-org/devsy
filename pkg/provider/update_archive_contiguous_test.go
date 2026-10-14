package provider

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/extract"
	"github.com/stretchr/testify/require"
)

func TestExtractConfinedUpdateTarPreservesContiguousFiles(t *testing.T) {
	archive := contiguousUpdateArchive(t)
	wantExecutable := []byte("#!/bin/sh\necho contiguous\n")
	wantSidecar := []byte("provider metadata\n")

	legacyDir := t.TempDir()
	require.NoError(t, extract.Extract(bytes.NewReader(archive), legacyDir))
	// #nosec G304 -- legacyDir and archive are owned entirely by this fixture.
	legacyExecutable, err := os.ReadFile(filepath.Join(legacyDir, "bin", "runtime"))
	require.NoError(t, err)
	require.Equal(t, wantExecutable, legacyExecutable)
	// #nosec G304 -- legacyDir and archive are owned entirely by this fixture.
	legacySidecar, err := os.ReadFile(filepath.Join(legacyDir, "bin", "provider.json"))
	require.NoError(t, err)
	require.Equal(t, wantSidecar, legacySidecar)

	destination := filepath.Join(t.TempDir(), "provider")
	require.NoError(t, os.Mkdir(destination, 0o700))
	root, err := os.OpenRoot(destination)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, root.Close()) })

	require.NoError(t, extractConfinedUpdateTar(
		context.Background(), root, bytes.NewReader(archive),
	))
	require.NoError(t, finalizeUpdateArchive(
		context.Background(), root, updateArchiveExecutablePath,
	))

	executable, err := root.ReadFile(updateArchiveExecutablePath)
	require.NoError(t, err)
	require.Equal(t, wantExecutable, executable)
	executableInfo, err := root.Stat(updateArchiveExecutablePath)
	require.NoError(t, err)
	assertUpdateArchiveFileMode(t, executableInfo, 0o755)

	sidecar, err := root.ReadFile(updateArchiveSidecarPath)
	require.NoError(t, err)
	require.Equal(t, wantSidecar, sidecar)
	sidecarInfo, err := root.Stat(updateArchiveSidecarPath)
	require.NoError(t, err)
	assertUpdateArchiveFileMode(t, sidecarInfo, 0o640)
}

func contiguousUpdateArchive(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	executable := []byte("#!/bin/sh\necho contiguous\n")
	require.NoError(t, writer.WriteHeader(&tar.Header{
		Name: updateArchiveExecutablePath, Typeflag: tar.TypeCont, Mode: 0o755,
		Size: int64(len(executable)), Format: tar.FormatGNU,
	}))
	_, err := writer.Write(executable)
	require.NoError(t, err)
	sidecar := []byte("provider metadata\n")
	require.NoError(t, writer.WriteHeader(&tar.Header{
		Name: updateArchiveSidecarPath, Typeflag: tar.TypeCont, Mode: 0o640,
		Size: int64(len(sidecar)), Format: tar.FormatGNU,
	}))
	_, err = writer.Write(sidecar)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	reader := tar.NewReader(bytes.NewReader(buffer.Bytes()))
	for _, expected := range []string{updateArchiveExecutablePath, updateArchiveSidecarPath} {
		header, err := reader.Next()
		require.NoError(t, err)
		require.Equal(t, expected, header.Name)
		require.Equal(t, byte(tar.TypeCont), header.Typeflag)
	}
	return buffer.Bytes()
}
