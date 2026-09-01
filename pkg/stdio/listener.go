package stdio

import (
	"io"
	"net"
	"sync"
)

// StdioListener implements the listener interface for one stdio connection.
type StdioListener struct {
	conn net.Conn

	mu       sync.Mutex
	accepted bool
	closed   bool
	closedCh chan struct{}
	once     sync.Once
}

// NewStdioListener creates a new one-shot stdio listener.
func NewStdioListener(reader io.Reader, writer io.WriteCloser) *StdioListener {
	lis := &StdioListener{
		closedCh: make(chan struct{}),
	}
	lis.conn = newStdioStream(reader, writer, lis.markClosed)
	return lis
}

// Ready implements interface.
func (lis *StdioListener) Ready(conn net.Conn) {
}

// Accept implements interface.
func (lis *StdioListener) Accept() (net.Conn, error) {
	lis.mu.Lock()
	if lis.closed {
		lis.mu.Unlock()
		return nil, net.ErrClosed
	}
	if !lis.accepted {
		lis.accepted = true
		conn := lis.conn
		lis.mu.Unlock()
		return conn, nil
	}
	lis.mu.Unlock()

	<-lis.closedCh
	return nil, net.ErrClosed
}

// Close closes the listener and its stdio connection.
func (lis *StdioListener) Close() error {
	lis.once.Do(func() {
		lis.mu.Lock()
		lis.closed = true
		close(lis.closedCh)
		lis.mu.Unlock()
	})

	if lis.conn != nil {
		_ = lis.conn.Close()
	}
	return nil
}

func (lis *StdioListener) markClosed() {
	lis.once.Do(func() {
		lis.mu.Lock()
		lis.closed = true
		close(lis.closedCh)
		lis.mu.Unlock()
	})
}

// Addr implements interface.
func (lis *StdioListener) Addr() net.Addr {
	return NewStdinAddr("listener")
}
