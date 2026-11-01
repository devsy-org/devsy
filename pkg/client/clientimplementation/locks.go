package clientimplementation

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/gofrs/flock"
)

const (
	lockTimeout = 5 * time.Minute
	lockRetry   = time.Second
)

// clientLocks lazily opens the file locks that serialize operations on a workspace.
type clientLocks struct {
	// ids returns the context, workspace ID and machine ID used for the lock files.
	// It is called once, on first use. An empty machine ID means no machine lock.
	ids func() (contextName, workspaceID, machineID string)

	once      sync.Once
	initErr   error
	workspace *flock.Flock
	machine   *flock.Flock
}

func newClientLocks(ids func() (string, string, string)) *clientLocks {
	return &clientLocks{ids: ids}
}

// Lock acquires the workspace lock, then the machine lock when one is configured.
func (l *clientLocks) Lock(ctx context.Context) error {
	if err := l.ensureOpen(); err != nil {
		return fmt.Errorf("init lock: %w", err)
	}

	log.Debug("acquire workspace lock")
	if err := l.lockWorkspace(ctx); err != nil {
		return fmt.Errorf("lock workspace: %w", err)
	}
	log.Debug("acquired workspace lock")

	if l.machine != nil {
		log.Debug("acquire machine lock")
		if err := tryLock(ctx, l.machine, "machine"); err != nil {
			if uerr := l.unlockWorkspace(); uerr != nil {
				log.Warnf("unlock workspace: %v", uerr)
			}
			return fmt.Errorf("lock machine: %w", err)
		}
		log.Debug("acquired machine lock")
	}

	return nil
}

// Unlock releases the machine lock, then the workspace lock.
func (l *clientLocks) Unlock() {
	if err := l.ensureOpen(); err != nil {
		log.Warnf("init lock: %v", err)
		return
	}

	if l.machine != nil {
		if err := l.machine.Unlock(); err != nil {
			log.Warnf("unlock machine: %v", err)
		}
	}
	if err := l.unlockWorkspace(); err != nil {
		log.Warnf("unlock workspace: %v", err)
	}
}

func (l *clientLocks) ensureOpen() error {
	l.once.Do(func() {
		contextName, workspaceID, machineID := l.ids()

		dir, err := provider.GetLocksDir(contextName)
		if err != nil {
			l.initErr = fmt.Errorf("get workspaces dir: %w", err)
			return
		}

		// #nosec G301 -- TODO Consider using a more secure permission setting and ownership if needed.
		if err := os.MkdirAll(dir, 0o755); err != nil {
			l.initErr = fmt.Errorf("create workspace locks dir: %w", err)
			return
		}

		l.workspace = flock.New(filepath.Join(dir, workspaceID+".workspace.lock"))
		if machineID != "" {
			l.machine = flock.New(filepath.Join(dir, machineID+".machine.lock"))
		}
	})

	return l.initErr
}

// lockWorkspace blocks until the workspace lock is held. ensureOpen must have succeeded.
func (l *clientLocks) lockWorkspace(ctx context.Context) error {
	return tryLock(ctx, l.workspace, "workspace")
}

// unlockWorkspace releases the workspace lock. ensureOpen must have succeeded.
func (l *clientLocks) unlockWorkspace() error {
	return l.workspace.Unlock()
}

func tryLock(ctx context.Context, lock *flock.Flock, name string) error {
	done := scheduleLogMessage(
		fmt.Sprintf(
			"Trying to lock %s, seems like another process is running that blocks this %s",
			name,
			name,
		),
	)
	defer close(done)

	now := time.Now()
	for time.Since(now) < lockTimeout {
		locked, err := lock.TryLock()
		if err != nil {
			return err
		} else if locked {
			return nil
		}

		select {
		case <-time.After(lockRetry):
			continue
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return fmt.Errorf(
		"timed out waiting to lock %s, seems like there is another process running on this machine that blocks it",
		name,
	)
}
