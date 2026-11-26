package terminal

import (
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type TerminalSuite struct {
	suite.Suite
}

func TestTerminalSuite(t *testing.T) {
	suite.Run(t, new(TerminalSuite))
}

func (s *TerminalSuite) TestAttachedRejectsPipes() {
	inR, inW, err := os.Pipe()
	s.Require().NoError(err)
	defer func() { s.NoError(inR.Close()) }()
	defer func() { s.NoError(inW.Close()) }()
	outR, outW, err := os.Pipe()
	s.Require().NoError(err)
	defer func() { s.NoError(outR.Close()) }()
	defer func() { s.NoError(outW.Close()) }()
	s.ErrorIs(Attached(inR, outW), ErrNotAttached)
}

func (s *TerminalSuite) TestInputCancelUnblocksRead() {
	if runtime.GOOS == "windows" {
		s.T().Skip("pipe reads are not cancellable on Windows")
	}
	r, w, err := os.Pipe()
	s.Require().NoError(err)
	defer func() { s.NoError(r.Close()) }()
	defer func() { s.NoError(w.Close()) }()
	input, err := NewInput(r)
	s.Require().NoError(err)
	done := make(chan error, 1)
	go func() {
		_, readErr := input.Read(make([]byte, 1))
		done <- readErr
	}()
	s.Eventually(func() bool { return input.Cancel() }, time.Second, time.Millisecond)
	var readErr error
	s.Eventually(func() bool {
		select {
		case readErr = <-done:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)
	s.Error(readErr)
	_, err = w.Write([]byte("x"))
	s.Require().NoError(err)
	buf := make([]byte, 1)
	_, err = input.Read(buf)
	s.Error(err)
	fresh, err := NewInput(r)
	s.Require().NoError(err)
	n, err := fresh.Read(buf)
	s.NoError(err)
	s.Equal(1, n)
	s.Equal(byte('x'), buf[0])
	s.NoError(input.Close())
	s.NoError(fresh.Close())
}

func (s *TerminalSuite) TestRestoreRegistry() {
	for range 100 {
		restore := registerRestore(func() {})
		restore()
		restore()
	}
	restores.Lock()
	s.Empty(restores.items)
	restores.Unlock()
	var order []int
	for i := range 3 {
		registerRestore(func() { order = append(order, i) })
	}
	RestoreAll()
	s.Equal([]int{2, 1, 0}, order)
	restores.Lock()
	s.Empty(restores.items)
	restores.Unlock()
}
