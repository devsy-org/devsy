package workspacelogs

import (
	"bytes"
	"fmt"
	"regexp"
	"sync"
)

var cursorPattern = regexp.MustCompile(`^[A-Za-z0-9_-]*$`)

// ValidateReadSelectors checks the values a caller may pass to a reader before
// any I/O. Every side of a refresh calls it again, so a hand-built command line
// cannot smuggle a path, a flag or an oversized cursor through.
func ValidateReadSelectors(req ReadRequest) error {
	if err := requireID("runId", req.RunID); err != nil {
		return err
	}
	if err := validateFilterSelectors(req); err != nil {
		return err
	}
	if len(req.After) > MaxCursorBytes || !cursorPattern.MatchString(req.After) {
		return fmt.Errorf("%w: malformed cursor", ErrInvalidCursor)
	}
	if req.Limit < 0 || req.Limit > MaxReadLimit {
		return fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidSelector, MaxReadLimit)
	}

	return nil
}

// BoundedBuffer keeps at most Max bytes of what is written and drops the rest.
// Writes never fail, so it is safe as a diagnostics sink.
type BoundedBuffer struct {
	Max int

	mu  sync.Mutex
	buf bytes.Buffer
}

// Write keeps the head of the stream.
func (b *BoundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := b.Max - b.buf.Len(); room > 0 {
		b.buf.Write(p[:min(room, len(p))])
	}

	return len(p), nil
}

// String returns the kept bytes.
func (b *BoundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}
