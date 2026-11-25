package session

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/devsy-org/devsy/pkg/pool/poolconfig"
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
func (l Layout) KeysDir() string     { return filepath.Join(l.dir, keysName) }
func (l Layout) AuditDir() string    { return filepath.Join(l.dir, "audit") }
func (l Layout) SocketPath() string  { return filepath.Join(l.dir, "pool.sock") }

// Lock is the pool's single-owner lock. It keeps the pool directory open and
// locks the lock file opened inside it, so what a Store works on is the
// directory that was locked, and it remembers that file so a Store can tell
// when the pool path no longer leads to it. The lock file must stay in place
// while the pool is active. Stores opened on a Lock share its mutex, and stop
// reading and writing once it is released.
type Lock struct {
	mu       sync.Mutex
	file     *os.File
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
	before, err := os.Lstat(dir)
	if err != nil {
		return nil, fmt.Errorf("stat pool directory: %w", err)
	}
	if !before.IsDir() {
		return nil, errNotDirectory
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open pool directory: %w", err)
	}
	if !samePoolRoot(dir, root, before) {
		_ = root.Close()
		return nil, errForeignLock
	}
	if err := checkRoot(root); err != nil {
		_ = root.Close()
		return nil, err
	}
	return root, nil
}

func samePoolRoot(dir string, root *os.Root, before fs.FileInfo) bool {
	after, err := os.Lstat(dir)
	own, ownErr := root.Stat(".")
	return err == nil && ownErr == nil && os.SameFile(before, own) && os.SameFile(after, own)
}

func openStoreRoot(pool *os.Root, name string) (*os.Root, error) {
	if err := ensureStoreDir(pool, name); err != nil {
		return nil, err
	}
	root, err := pool.OpenRoot(name)
	if err != nil {
		return nil, fmt.Errorf("open %s directory: %w", name, err)
	}
	if err := prepareStoreRoot(pool, root, name); err != nil {
		_ = root.Close()
		return nil, err
	}
	return root, nil
}

func prepareStoreRoot(pool, root *os.Root, name string) error {
	if !inPool(pool, root, name) {
		return errStoreMoved
	}
	if err := checkRoot(root); err != nil {
		return err
	}
	return removeStrayTemps(root)
}

func ensureStoreDir(pool *os.Root, name string) error {
	info, err := pool.Lstat(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return makeStoreDir(pool, name)
	case err != nil:
		return fmt.Errorf("stat %s directory: %w", name, err)
	case !info.IsDir():
		return errNotDirectory
	}
	return nil
}

func makeStoreDir(pool *os.Root, name string) error {
	if err := pool.Mkdir(name, dirMode); err != nil {
		return fmt.Errorf("create %s directory: %w", name, err)
	}
	if err := syncRoot(pool); err != nil {
		return fmt.Errorf("sync pool directory: %w", err)
	}
	return nil
}

func inPool(pool, root *os.Root, name string) bool {
	entry, err := pool.Lstat(name)
	own, ownErr := root.Stat(".")
	return err == nil && ownErr == nil && entry.IsDir() && os.SameFile(entry, own)
}

// acquire opens the lock file inside the pinned directory and locks that open
// file, so the lock is on the file the directory holds.
func acquire(root *os.Root, dir string) (*Lock, error) {
	f, err := root.OpenFile(lockName, os.O_RDWR|os.O_CREATE, fileMode)
	if err != nil {
		return nil, fmt.Errorf("open pool lock file: %w", err)
	}
	k, err := lockOpenFile(f, root, dir)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return k, nil
}

// lockOpenFile takes the lock, then checks that the directory entry and the
// pool path still lead to the file that was locked.
func lockOpenFile(f *os.File, root *os.Root, dir string) (*Lock, error) {
	ok, err := tryLock(f)
	if err != nil {
		return nil, fmt.Errorf("lock pool directory: %w", err)
	}
	if !ok {
		return nil, ErrPoolLocked
	}
	info, err := f.Stat()
	entry, entryErr := root.Stat(lockName)
	if err != nil || entryErr != nil || !os.SameFile(info, entry) || !sameLockFile(dir, info) {
		return nil, errForeignLock
	}
	return &Lock{file: f, dir: dir, root: root, lockInfo: info}, nil
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
