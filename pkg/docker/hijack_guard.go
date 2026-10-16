package docker

import (
	"io"
	"sync"
)

type hijackAttemptKey struct{}

// hijackAttempt tracks the raw connection of one pending hijack negotiation so
// cancelling that attempt aborts only its own connection.
type hijackAttempt struct {
	mu      sync.Mutex
	conn    io.Closer
	done    bool
	aborted bool
}

// register reports false when the attempt was already aborted.
func (a *hijackAttempt) register(conn io.Closer) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.aborted {
		return false
	}
	a.conn = conn
	return true
}

func (a *hijackAttempt) abort() {
	a.mu.Lock()
	if a.done {
		a.mu.Unlock()
		return
	}
	a.aborted = true
	conn := a.conn
	a.mu.Unlock()

	if conn != nil {
		_ = conn.Close()
	}
}

// finish detaches the attempt and reports whether it had been aborted.
func (a *hijackAttempt) finish() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.done = true
	return a.aborted
}
