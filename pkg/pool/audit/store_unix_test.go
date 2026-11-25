//go:build linux || darwin || freebsd

package audit

import (
	"os"
	"path/filepath"
	"syscall"
	"time"
)

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
	real := filepath.Join(s.T().TempDir(), "real")
	s.Require().NoError(os.WriteFile(real, []byte("h_01ARZ3NDEKTSV4RRFFQ69G5FAV\n"), fileMode))
	s.Require().NoError(os.Symlink(real, path))
	_, err := LoadOrCreateHostID(path)
	s.Error(err, "a linked host id file must not be read")
	s.Require().NoError(os.Remove(path))
	s.Require().NoError(os.Symlink(filepath.Join(dir, "missing"), path))
	_, err = LoadOrCreateHostID(path)
	s.Error(err)
	target, err := os.Readlink(path)
	s.Require().NoError(err)
	s.Equal(filepath.Join(dir, "missing"), target, "a dangling link must not be replaced")
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
