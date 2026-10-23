//go:build linux || darwin

package dependencies

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/devsy-org/devsy/internal/dependencytest"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	metadataShortWrite  = "short-write"
	metadataCancelWrite = "cancel-write"
	metadataCancelSync  = "cancel-sync"
	metadataCancelClose = "cancel-close"
)

type MetadataWriterSuite struct{ suite.Suite }

func TestMetadataWriter(t *testing.T) { suite.Run(t, new(MetadataWriterSuite)) }

func metadataWriterFixture(t *testing.T) (*metadataWriter, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state")
	require.NoError(t, os.Mkdir(path, 0o700))
	root, err := os.OpenRoot(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, root.Close()) })
	writer, err := newMetadataWriter(root)
	require.NoError(t, err)
	return writer, path
}

func (s *MetadataWriterSuite) TestPrivateStateAndInitialization() {
	writer, path := metadataWriterFixture(s.T())
	entries, err := os.ReadDir(path)
	s.Require().NoError(err)
	s.Empty(entries, "constructor does not initialize the store")
	s.Error(writer.writeJournal(s.T().Context(), metadataJournalFixture()))
	s.Require().NoError(writer.initialize(s.T().Context()))
	s.Require().NoError(writer.writeRegistry(s.T().Context(), metadataRegistryFixture()))
	s.Require().NoError(writer.writeJournal(s.T().Context(), metadataJournalFixture()))
	for _, name := range []string{"registry.json", "operations/operation-123.json"} {
		info, err := os.Stat(filepath.Join(path, name))
		s.Require().NoError(err)
		s.Equal(os.FileMode(0o600), info.Mode().Perm())
	}
	info, err := os.Stat(filepath.Join(path, "operations"))
	s.Require().NoError(err)
	s.Equal(os.FileMode(0o700), info.Mode().Perm())
	missing := filepath.Join(path, "missing")
	_, err = os.OpenRoot(missing)
	s.ErrorIs(err, os.ErrNotExist)
	_, err = os.Stat(missing)
	s.ErrorIs(err, os.ErrNotExist)
	s.Require().NoError(writer.root.Chmod(".", 0o755))
	_, err = newMetadataWriter(writer.root)
	s.ErrorContains(err, "private")
}

func (s *MetadataWriterSuite) TestFailuresBeforeReplacement() {
	stages := []string{
		"write",
		metadataShortWrite,
		"sync",
		"close",
		"rename",
		metadataCancelWrite,
		metadataCancelSync,
		metadataCancelClose,
	}
	for _, stage := range stages {
		s.Run(stage, func() { s.assertFailureBeforeReplacement(stage) })
	}
}

func injectMetadataFailure(
	writer *metadataWriter,
	stage string,
	fault error,
	cancel context.CancelFunc,
) {
	switch stage {
	case "write":
		writer.hooks.write = func(*os.File, []byte) (int, error) { return 0, fault }
	case metadataShortWrite:
		writer.hooks.write = func(f *os.File, data []byte) (int, error) { return f.Write(data[:len(data)/2]) }
	case "sync":
		writer.hooks.sync = func(*os.File) error { return fault }
	case "close":
		writer.hooks.close = func(f *os.File) error { return errors.Join(f.Close(), fault) }
	case "rename":
		writer.hooks.rename = func(*os.Root, string, string) error { return fault }
	case metadataCancelWrite, metadataCancelSync, metadataCancelClose:
		injectMetadataCancellation(writer, stage, cancel)
	}
}

func (s *MetadataWriterSuite) TestPublishedDurabilityUncertainty() {
	for _, stage := range []string{"directory-sync", "cancel-after-rename"} {
		s.Run(stage, func() {
			writer, path := metadataWriterFixture(s.T())
			s.Require().NoError(writer.writeRegistry(s.T().Context(), metadataRegistryFixture()))
			ctx, cancel := context.WithCancel(s.T().Context())
			defer cancel()
			if stage == "directory-sync" {
				writer.hooks.syncDir = func(*os.Root) error { return errors.New("injected directory sync failure") }
			} else {
				writer.hooks.rename = func(root *os.Root, from, to string) error {
					err := root.Rename(from, to)
					cancel()
					return err
				}
			}
			next := metadataRegistryFixture()
			next.Revision++
			err := writer.writeRegistry(ctx, next)
			var commit *metadataCommitError
			s.Require().ErrorAs(err, &commit)
			s.True(commit.Published)
			data, err := writer.root.ReadFile("registry.json")
			s.Require().NoError(err)
			decoded, err := DecodeRegistry(bytes.NewReader(data))
			s.Require().NoError(err)
			s.Equal(next, decoded)
			entries, err := os.ReadDir(path)
			s.Require().NoError(err)
			s.Len(entries, 1)
		})
	}
}

func (s *MetadataWriterSuite) TestFutureAndCorruptStatePreserved() {
	originals := []string{
		`{"schemaVersion":42,"future":"must-survive"}`, `{"schemaVersion":42,"future":null}`,
		`{"schemaVersion":1,"revision":1}`, "broken",
	}
	for _, original := range originals {
		writer, path := metadataWriterFixture(s.T())
		target := filepath.Join(path, "registry.json")
		s.Require().NoError(os.WriteFile(target, []byte(original), 0o600))
		err := writer.writeRegistry(s.T().Context(), metadataRegistryFixture())
		s.Require().Error(err)
		if strings.Contains(original, `"schemaVersion":42`) {
			var future *UnsupportedMetadataSchemaError
			s.Require().ErrorAs(err, &future)
			s.Equal(42, future.Version)
		}
		after, err := writer.root.ReadFile("registry.json")
		s.Require().NoError(err)
		s.Equal(original, string(after))
	}
}

func (s *MetadataWriterSuite) TestDestinationAndParentSymlinks() {
	external := s.T().TempDir()
	sentinel := filepath.Join(external, "sentinel")
	s.Require().NoError(os.WriteFile(sentinel, []byte("external runtime"), 0o600))
	dependencytest.Preserve(s.T(), external)
	writer, path := metadataWriterFixture(s.T())
	s.Require().NoError(os.Symlink(sentinel, filepath.Join(path, "registry.json")))
	s.Error(writer.writeRegistry(s.T().Context(), metadataRegistryFixture()))
	s.Require().NoError(os.Remove(filepath.Join(path, "registry.json")))
	s.Require().NoError(os.Symlink(external, filepath.Join(path, "operations")))
	s.Error(writer.initialize(s.T().Context()))
	s.Error(writer.writeJournal(s.T().Context(), metadataJournalFixture()))
}

func (s *MetadataWriterSuite) TestSubstitutionsAfterOpenAndBeforeRename() {
	external := s.T().TempDir()
	sentinel := filepath.Join(external, "sentinel")
	s.Require().NoError(os.WriteFile(sentinel, []byte("external runtime"), 0o600))
	dependencytest.Preserve(s.T(), external)
	writer, path := metadataWriterFixture(s.T())
	moved := path + "-pinned"
	s.Require().NoError(os.Rename(path, moved))
	s.T().Cleanup(func() { s.Require().NoError(os.RemoveAll(moved)) })
	s.Require().NoError(os.Symlink(external, path))
	writer.hooks.rename = func(root *os.Root, from, to string) error {
		if err := root.Symlink(sentinel, to); err != nil {
			return err
		}
		return root.Rename(from, to)
	}
	s.Require().NoError(writer.writeRegistry(s.T().Context(), metadataRegistryFixture()))
	data, err := writer.root.ReadFile("registry.json")
	s.Require().NoError(err)
	_, err = DecodeRegistry(bytes.NewReader(data))
	s.NoError(err)
}

func (s *MetadataWriterSuite) TestOperationsDirectoryPinnedDuringWrite() {
	external := s.T().TempDir()
	s.Require().NoError(os.WriteFile(filepath.Join(external, "sentinel"), []byte("keep"), 0o600))
	dependencytest.Preserve(s.T(), external)
	writer, path := metadataWriterFixture(s.T())
	s.Require().NoError(writer.initialize(s.T().Context()))
	writer.hooks.write = func(file *os.File, data []byte) (int, error) {
		if err := os.Rename(
			filepath.Join(path, "operations"),
			filepath.Join(path, "pinned"),
		); err != nil {
			return 0, err
		}
		if err := os.Symlink(external, filepath.Join(path, "operations")); err != nil {
			return 0, err
		}
		return file.Write(data)
	}
	s.Require().NoError(writer.writeJournal(s.T().Context(), metadataJournalFixture()))
	data, err := writer.root.ReadFile("pinned/operation-123.json")
	s.Require().NoError(err)
	_, err = DecodeJournal(bytes.NewReader(data))
	s.NoError(err)
}

func (s *MetadataWriterSuite) TestCancellationBeforeWriteHasNoEffects() {
	writer, path := metadataWriterFixture(s.T())
	ctx, cancel := context.WithCancel(s.T().Context())
	cancel()
	s.ErrorIs(writer.initialize(ctx), context.Canceled)
	s.ErrorIs(writer.writeRegistry(ctx, metadataRegistryFixture()), context.Canceled)
	entries, err := os.ReadDir(path)
	s.Require().NoError(err)
	s.Empty(entries)
}

func (s *MetadataWriterSuite) TestCleanupPreservesUnrelatedTemporaryFiles() {
	writer, path := metadataWriterFixture(s.T())
	unrelated := filepath.Join(path, ".metadata-someone-else.tmp")
	s.Require().NoError(os.WriteFile(unrelated, []byte("keep"), 0o600))
	writer.hooks.sync = func(*os.File) error { return errors.New("sync failed") }
	s.Error(writer.writeRegistry(s.T().Context(), metadataRegistryFixture()))
	data, err := writer.root.ReadFile(".metadata-someone-else.tmp")
	s.Require().NoError(err)
	s.Equal("keep", string(data))
	entries, err := os.ReadDir(path)
	s.Require().NoError(err)
	s.Len(entries, 1)
}

func (s *MetadataWriterSuite) TestAtomicReaders() {
	writer, _ := metadataWriterFixture(s.T())
	s.Require().NoError(writer.writeRegistry(s.T().Context(), metadataRegistryFixture()))
	var readers sync.WaitGroup
	results := make(chan error, 1)
	readers.Go(func() {
		for range 100 {
			data, err := writer.root.ReadFile("registry.json")
			if err == nil {
				_, err = DecodeRegistry(bytes.NewReader(data))
			}
			if err != nil {
				results <- err
				return
			}
		}
	})
	for revision := uint64(2); revision < 20; revision++ {
		next := metadataRegistryFixture()
		next.Revision = revision
		s.Require().NoError(writer.writeRegistry(s.T().Context(), next))
	}
	readers.Wait()
	close(results)
	for err := range results {
		s.NoError(err)
	}
}

func (s *MetadataWriterSuite) TestLogicalMetadataNames() {
	writer, _ := metadataWriterFixture(s.T())
	for _, destination := range []metadataDestination{
		{root: writer.root, name: metadataTraversalPath},
		{root: writer.root, name: "receipt.json"},
		{root: writer.root, name: "/registry.json"},
		{root: writer.root, name: "con.json", journal: true},
		{root: writer.root, name: "../operation.json", journal: true},
	} {
		s.Error(writer.replace(s.T().Context(), destination, []byte("invalid")))
	}
	s.Require().NoError(writer.initialize(s.T().Context()))
	journal := metadataJournalFixture()
	journal.OperationID = "registry"
	s.Require().NoError(writer.writeJournal(s.T().Context(), journal))
	journal.Phase = PhaseDownloading
	s.NoError(writer.writeJournal(s.T().Context(), journal))
}

func (s *MetadataWriterSuite) assertFailureBeforeReplacement(stage string) {
	writer, path := metadataWriterFixture(s.T())
	old := metadataRegistryFixture()
	s.Require().NoError(writer.writeRegistry(s.T().Context(), old))
	oldData, err := writer.root.ReadFile("registry.json")
	s.Require().NoError(err)
	ctx, cancel := context.WithCancel(s.T().Context())
	defer cancel()
	sentinel := errors.New("injected " + stage)
	injectMetadataFailure(writer, stage, sentinel, cancel)
	next := old
	next.Revision++
	err = writer.writeRegistry(ctx, next)
	s.Require().Error(err)
	expected := sentinel
	if stage == metadataShortWrite {
		expected = io.ErrShortWrite
	}
	if strings.HasPrefix(stage, "cancel-") {
		expected = context.Canceled
	}
	s.ErrorIs(err, expected)
	var commit *metadataCommitError
	s.False(errors.As(err, &commit))
	after, err := writer.root.ReadFile("registry.json")
	s.Require().NoError(err)
	s.Equal(oldData, after)
	entries, err := os.ReadDir(path)
	s.Require().NoError(err)
	s.Len(entries, 1, "only this attempt's temporary file is removed")
}

func injectMetadataCancellation(writer *metadataWriter, stage string, cancel context.CancelFunc) {
	switch stage {
	case metadataCancelWrite:
		writer.hooks.write = func(f *os.File, data []byte) (int, error) { n, err := f.Write(data); cancel(); return n, err }
	case metadataCancelSync:
		writer.hooks.sync = func(f *os.File) error { err := f.Sync(); cancel(); return err }
	case metadataCancelClose:
		writer.hooks.close = func(f *os.File) error { err := f.Close(); cancel(); return err }
	}
}
