package external

import (
	"errors"
	"io"
	"sync"
)

// Serialize failure recording with owned closure: errors observed before Close
// survive stream completion; errors from unblocking Read during Close do not.
type execInput struct {
	reader io.Reader
	mu     sync.Mutex
	closed bool
	err    error
}

func (r *execInput) Read(data []byte) (int, error) {
	if r.reader == nil {
		return 0, io.EOF
	}
	n, err := r.reader.Read(data)
	if err != nil && !errors.Is(err, io.EOF) {
		r.mu.Lock()
		if !r.closed {
			r.err = err
		}
		r.mu.Unlock()
	}
	return n, err
}

func (r *execInput) close() {
	if closer, ok := r.reader.(io.Closer); ok {
		r.mu.Lock()
		r.closed = true
		r.mu.Unlock()
		_ = closer.Close()
	}
}

func (r *execInput) readError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}
