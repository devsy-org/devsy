package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

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

// Lock is the pool's single-owner lock.
type Lock struct{ file *flock.Flock }

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
	return &Lock{file: file}, nil
}

// Release drops the lock.
func (k *Lock) Release() error { return k.file.Close() }
