package session

import (
	"errors"
	"fmt"
	"io/fs"
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
func (l Layout) SessionsDir() string { return filepath.Join(l.dir, sessionsName) }
func (l Layout) KeysDir() string     { return filepath.Join(l.dir, "keys") }
func (l Layout) AuditDir() string    { return filepath.Join(l.dir, "audit") }
func (l Layout) SocketPath() string  { return filepath.Join(l.dir, "pool.sock") }

// Lock is the pool's single-owner lock. It keeps the pool directory open, so
// what a Store writes to is the directory that was locked, and it remembers
// the lock file so a Store can tell when the path no longer leads to it. The
// lock file must stay in place while the pool is active. Stores opened on a
// Lock share its mutex and stop writing once it is released.
type Lock struct {
	mu       sync.Mutex
	file     *flock.Flock
	dir      string
	root     *os.Root
	lockInfo fs.FileInfo
	released bool
}

// Lock takes the pool lock without waiting. It creates the pool directory
// when needed and refuses one that other users can write to.
func (l Layout) Lock() (*Lock, error) {
	if err := checkPlatform(); err != nil {
		return nil, err
	}
	dir, err := filepath.Abs(l.dir)
	if err != nil {
		return nil, fmt.Errorf("resolve pool directory: %w", err)
	}
	root, err := openPoolRoot(dir)
	if err != nil {
		return nil, err
	}
	k, err := acquire(root, dir)
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	return k, nil
}

func openPoolRoot(dir string) (*os.Root, error) {
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return nil, fmt.Errorf("create pool directory: %w", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open pool directory: %w", err)
	}
	if err := checkRoot(root); err != nil {
		_ = root.Close()
		return nil, err
	}
	return root, nil
}

// acquire locks the file by path, then checks that the path still leads to
// the lock file inside the directory held open.
func acquire(root *os.Root, dir string) (*Lock, error) {
	file := flock.New(filepath.Join(dir, lockName))
	ok, err := file.TryLock()
	if err != nil {
		return nil, fmt.Errorf("lock pool directory: %w", err)
	}
	if !ok {
		return nil, ErrPoolLocked
	}
	info, err := root.Stat(lockName)
	if err != nil || !sameLockFile(dir, info) {
		_ = file.Close()
		return nil, errForeignLock
	}
	return &Lock{file: file, dir: dir, root: root, lockInfo: info}, nil
}

func sameLockFile(dir string, want fs.FileInfo) bool {
	cur, err := os.Stat(filepath.Join(dir, lockName))
	return err == nil && os.SameFile(cur, want)
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
	return errors.Join(k.file.Close(), k.root.Close())
}

// hold takes the write mutex and refuses once the lock is released or the
// pool path no longer leads to the lock file this Lock took. The caller runs
// the returned function when its write is done.
func (k *Lock) hold() (func(), error) {
	if k == nil {
		return nil, errLockNotHeld
	}
	k.mu.Lock()
	if k.released || !sameLockFile(k.dir, k.lockInfo) {
		k.mu.Unlock()
		return nil, errLockNotHeld
	}
	return k.mu.Unlock, nil
}

// covers checks that dir is the sessions directory of the locked pool
// directory and that pool is that directory's name. The caller holds the lock.
func (k *Lock) covers(dir, pool string) error {
	clean := filepath.Clean(dir)
	parent, err := os.Stat(filepath.Dir(clean))
	own, ownErr := k.root.Stat(".")
	if err != nil || ownErr != nil || !os.SameFile(parent, own) ||
		filepath.Base(clean) != sessionsName || pool != filepath.Base(k.dir) {
		return errForeignLock
	}
	return nil
}
