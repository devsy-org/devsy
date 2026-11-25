//go:build linux || darwin || freebsd

package audit

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

func (s *StoreSuite) syncFailsOnForeignDirs(err error) {
	if os.Geteuid() == 0 {
		s.T().Skip("every directory is owned by the current user")
	}
	orig := syncDirFunc
	s.T().Cleanup(func() { syncDirFunc = orig })
	syncDirFunc = func(dir string) error {
		if ownedByCurrentUser(dir) {
			return nil
		}
		return err
	}
}

func (s *StoreSuite) TestOpenFailsWhenAForeignParentCannotBeSynced() {
	s.syncFailsOnForeignDirs(errors.New("injected sync failure"))
	_, err := Open(s.dir)
	s.ErrorContains(err, "injected sync failure")
}

func (s *StoreSuite) TestOpenFailsWhenAForeignParentCannotBeRead() {
	s.syncFailsOnForeignDirs(fs.ErrPermission)
	_, err := Open(s.dir)
	s.ErrorIs(err, fs.ErrPermission)
}

func (s *StoreSuite) TestHostIDFifoIsRefusedWithoutHanging() {
	path := filepath.Join(s.T().TempDir(), "host-id")
	s.Require().NoError(syscall.Mkfifo(path, fileMode))
	done := make(chan error, 1)
	go func() {
		_, err := LoadOrCreateHostID(path)
		done <- err
	}()
	select {
	case err := <-done:
		s.Error(err)
	case <-time.After(3 * time.Second):
		s.FailNow("host id read blocked on a FIFO")
	}
}

func (s *StoreSuite) TestHostIDSymlinkIsRefusedAndKept() {
	dir := s.T().TempDir()
	path := filepath.Join(dir, "host-id")
	target := filepath.Join(s.T().TempDir(), "real")
	s.Require().NoError(os.WriteFile(target, []byte("h_01ARZ3NDEKTSV4RRFFQ69G5FAV\n"), fileMode))
	s.Require().NoError(os.Symlink(target, path))
	_, err := LoadOrCreateHostID(path)
	s.Error(err, "a linked host id file must not be read")
	s.Require().NoError(os.Remove(path))
	s.Require().NoError(os.Symlink(filepath.Join(dir, "missing"), path))
	_, err = LoadOrCreateHostID(path)
	s.Error(err)
	linked, err := os.Readlink(path)
	s.Require().NoError(err)
	s.Equal(filepath.Join(dir, "missing"), linked, "a dangling link must not be replaced")
}

func (s *StoreSuite) TestHostIDRefusesSharedDirectory() {
	dir := s.T().TempDir()
	s.Require().NoError(os.Chmod(dir, 0o777)) //nolint:gosec // the test needs a shared directory
	_, err := LoadOrCreateHostID(filepath.Join(dir, "host-id"))
	s.ErrorContains(err, "must not be writable")
}

func (s *StoreSuite) TestOpenSnapshotRefusesFifo() {
	st := s.open()
	s.Require().NoError(syscall.Mkfifo(filepath.Join(s.dir, dayOne+fileExt), fileMode))
	done := make(chan error, 1)
	go func() {
		_, err := st.openSnapshot(dayOne)
		done <- err
	}()
	select {
	case err := <-done:
		s.Error(err)
	case <-time.After(3 * time.Second):
		s.FailNow("snapshot open blocked on a FIFO")
	}
}
