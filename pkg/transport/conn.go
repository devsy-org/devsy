package transport

import (
	"net"
	"sync"
)

// ManagedConn is a net.Conn with an observable terminal result.
type ManagedConn interface {
	net.Conn
	Wait() error
}

// CloseWriter is optionally implemented by transports that support half-close.
type CloseWriter interface {
	CloseWrite() error
}

// ProcessInfo is optionally implemented by process-backed transports.
type ProcessInfo interface {
	PID() int
}

type managedState struct {
	closeOnce sync.Once
	waitOnce  sync.Once
	waitDone  chan struct{}
	waitErr   error
}

func newManagedState() *managedState {
	return &managedState{waitDone: make(chan struct{})}
}

func (s *managedState) finish(err error) {
	s.waitOnce.Do(func() {
		s.waitErr = err
		close(s.waitDone)
	})
}

func (s *managedState) wait() error {
	<-s.waitDone
	return s.waitErr
}
