//go:build !windows

package credstore

import (
	"syscall"
	"time"
)

func (s *StoreSuite) TestLoadRefusesFIFOWithoutBlocking() {
	s.Require().NoError(syscall.Mkfifo(s.file(), 0o600))
	done := make(chan error, 1)

	go func() {
		_, err := s.store().Load()
		done <- err
	}()

	select {
	case err := <-done:
		s.Require().Error(err)
		s.Contains(err.Error(), "not a regular file")
	case <-time.After(5 * time.Second):
		s.FailNow("Load blocked on a FIFO")
	}
}
