package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/devsy-org/devsy/pkg/pool/poolconfig"
	"github.com/gofrs/flock"
)

const lockName = "pool.lock"

// ErrPoolLocked means another process owns this pool's directory.
var ErrPoolLocked = errors.New("another process holds the pool lock")

// Layout names the files of one pool under <root>/<pool>.
type Layout struct{ dir string }

// NewLayout checks the pool name, which becomes a directory name.
func NewLayout(root, pool string) (Layout, error) {
	if !poolconfig.ValidPoolName(pool) {
		return Layout{}, errInvalidPool
	}
	return Layout{dir: filepath.Join(root, pool)}, nil
}

func (l Layout) Dir() string         { return l.dir }
func (l Layout) SessionsDir() string { return filepath.Join(l.dir, "sessions") }
func (l Layout) KeysDir() string     { return filepath.Join(l.dir, "keys") }
func (l Layout) AuditDir() string    { return filepath.Join(l.dir, "audit") }
func (l Layout) SocketPath() string  { return filepath.Join(l.dir, "pool.sock") }

// Lock is the pool's single-owner lock. Stores opened on it share its mutex
// and stop writing once it is released.
type Lock struct {
	mu       sync.Mutex
	file     *flock.Flock
	dir      string
	released bool
}

// Lock takes the pool lock without waiting. It creates the pool directory
// when needed and refuses one that other users can write to.
func (l Layout) Lock() (*Lock, error) {
	if err := checkPlatform(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(l.dir, dirMode); err != nil {
		return nil, fmt.Errorf("create pool directory: %w", err)
	}
	if err := checkDirMode(l.dir); err != nil {
		return nil, err
	}
	file := flock.New(filepath.Join(l.dir, lockName))
	ok, err := file.TryLock()
	if err != nil {
		return nil, fmt.Errorf("lock pool directory: %w", err)
	}
	if !ok {
		return nil, ErrPoolLocked
	}
	return &Lock{file: file, dir: l.dir}, nil
}

// Release drops the lock after any write in flight finishes. It is safe to
// call twice.
func (k *Lock) Release() error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.released {
		return nil
	}
	k.released = true
	return k.file.Close()
}

// hold takes the write mutex and refuses once the lock is released. The
// caller runs the returned function when its write is done.
func (k *Lock) hold() (func(), error) {
	if k == nil {
		return nil, errLockNotHeld
	}
	k.mu.Lock()
	if k.released {
		k.mu.Unlock()
		return nil, errLockNotHeld
	}
	return k.mu.Unlock, nil
}

// covers checks that dir is a direct child of the locked pool directory and
// that pool is that directory's name.
func (k *Lock) covers(dir, pool string) error {
	if k == nil {
		return errLockNotHeld
	}
	parent, perr := filepath.EvalSymlinks(filepath.Dir(filepath.Clean(dir)))
	own, oerr := filepath.EvalSymlinks(k.dir)
	if perr != nil || oerr != nil || parent != own || pool != filepath.Base(k.dir) {
		return errForeignLock
	}
	return nil
}
