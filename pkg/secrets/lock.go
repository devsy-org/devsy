package secrets

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

const (
	flockTimeout    = 15 * time.Second
	flockRetryDelay = 50 * time.Millisecond
)

// acquireFlock takes an exclusive cross-process lock on <dir>/<name> and returns
// a release func that is always safe to call. It gives up with a clear error
// after flockTimeout rather than blocking forever behind a stuck holder.
//
// NOT reentrant: each call opens its own flock handle, so taking a lock while
// one is already held on the same path in this process self-deadlocks.
func acquireFlock(dir, name string) (func(), error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create secrets dir: %w", err)
	}
	l := flock.New(filepath.Join(dir, name))

	ctx, cancel := context.WithTimeout(context.Background(), flockTimeout)
	defer cancel()
	locked, err := l.TryLockContext(ctx, flockRetryDelay)
	if err != nil {
		return nil, fmt.Errorf("lock %s: %w", name, err)
	}
	if !locked {
		return nil, fmt.Errorf("lock %s: timed out after %s", name, flockTimeout)
	}

	return func() { _ = l.Unlock() }, nil
}
