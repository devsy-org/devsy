package machinediagnostics

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
)

const (
	DefaultRuntimeLockPath = "/run/devsy/agent-daemon.lock"
	RuntimeLockPathEnv     = "DEVSY_DAEMON_RUNTIME_LOCK_PATH"
)

// RuntimeLock prevents two singleton machine daemons from supervising the
// same host. Its lifetime is the daemon process lifetime.
type RuntimeLock struct{ lock *flock.Flock }

func AcquireRuntimeLock(path string) (*RuntimeLock, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("daemon runtime lock path must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create daemon runtime directory: %w", err)
	}
	lock := flock.New(path)
	locked, err := lock.TryLock()
	if err != nil {
		return nil, fmt.Errorf("lock daemon runtime: %w", err)
	}
	if !locked {
		return nil, fmt.Errorf("a Devsy machine daemon is already running")
	}
	return &RuntimeLock{lock: lock}, nil
}

func (l *RuntimeLock) Close() error {
	if l == nil || l.lock == nil {
		return nil
	}
	if err := l.lock.Unlock(); err != nil {
		return fmt.Errorf("unlock daemon runtime: %w", err)
	}
	return nil
}
