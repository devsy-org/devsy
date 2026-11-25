package session

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/pool/poolconfig"
)

const (
	fileExt      = ".json"
	sessionsName = "sessions"
	keysName     = "keys"
	tempPrefix   = ".tmp-"
	maxFileBytes = 64 << 10
	dirMode      = 0o700
	fileMode     = 0o600
	idAttempts   = 3
	tempAttempts = 10
)

// Errors a caller can test with errors.Is.
var (
	ErrNotFound          = errors.New("session not found")
	ErrExists            = errors.New("session already exists")
	ErrStateConflict     = errors.New("session is not in the expected state")
	ErrIllegalTransition = errors.New("session state transition is not allowed")

	errUnexpectedEntry = errors.New("unexpected entry in the record directory")
	errLockNotHeld     = errors.New("the pool lock is not held")
	errForeignLock     = errors.New("the pool lock belongs to another pool directory")
	errNotDirectory    = errors.New("pool path is not a directory")
	errStoreMoved      = errors.New("a record directory is no longer in the pool directory")
	errNotRegular      = errors.New("record path is not a regular file")
	errTooLarge        = fmt.Errorf("record file is longer than %d bytes", maxFileBytes)
)

// Store keeps session and idempotency records in pinned directories. Writes
// publish synced files by link or rename. Each operation except Close takes
// the lock mutex and checks the lock and both directories before using them, so
// a read never sees a write half done and Release waits for a call in flight.
// Load and List need the live lock; a read-only inspector needs a separate
// path. OpenStore removes regular files named .tmp-<digits> in the sessions and
// keys directories while holding the lock.
type Store struct {
	lock *Lock
	root *os.Root
	keys *os.Root
	pool string
	now  func() time.Time
}

// OpenStore opens or creates the sessions and keys directories of the locked
// pool. While holding the lock, it removes regular files named .tmp-<digits>
// in the sessions and keys directories. dir must be the locked pool's sessions
// directory. Neither subdirectory may be a symlink. The caller closes the Store.
func OpenStore(dir, pool string, lock *Lock) (*Store, error) {
	if err := checkPlatform(); err != nil {
		return nil, err
	}
	if !poolconfig.ValidPoolName(pool) {
		return nil, errInvalidPool
	}
	unlock, err := lock.hold()
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := lock.covers(dir, pool); err != nil {
		return nil, err
	}
	return openLockedStore(lock, pool)
}

func openLockedStore(lock *Lock, pool string) (*Store, error) {
	root, err := openStoreRoot(lock.root, sessionsName)
	if err != nil {
		return nil, err
	}
	keys, err := openStoreRoot(lock.root, keysName)
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	store := &Store{lock: lock, root: root, keys: keys, pool: pool, now: time.Now}
	if err := syncRoot(lock.root); err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("sync pool directory: %w", err)
	}
	return store, nil
}

// Close releases both directory roots. It does not release the lock.
func (s *Store) Close() error { return errors.Join(s.root.Close(), s.keys.Close()) }

// Create writes a queued session without reserving an idempotency key or checking capacity.
// Production requests go through Admit.
func (s *Store) Create() (Session, error) {
	unlock, err := s.hold()
	if err != nil {
		return Session{}, err
	}
	defer unlock()
	id, err := s.freshID()
	if err != nil {
		return Session{}, err
	}
	return s.createQueued(id, s.now().UTC())
}

// Load reads one session.
func (s *Store) Load(id string) (Session, error) {
	unlock, err := s.hold()
	if err != nil {
		return Session{}, err
	}
	defer unlock()
	return s.load(id)
}

// List reads every session in ID order. A file that is not a valid session
// stops the listing: the pool cannot tell what state it holds.
func (s *Store) List() ([]Session, error) {
	unlock, err := s.hold()
	if err != nil {
		return nil, err
	}
	defer unlock()
	return s.list()
}

// Transition moves a session from one state to another. It refuses an edge
// the state machine does not allow, and a session that is not in from. On any
// error the file on disk is unchanged, except that a failed directory sync
// after the rename may leave the new state on disk. A caller must treat an
// error as "state unknown" and reload, not retry blindly.
// Production requests go through Promote for queued-to-spawning moves to enforce the active limit.
func (s *Store) Transition(id string, from, to State, reason string) (Session, error) {
	unlock, err := s.hold()
	if err != nil {
		return Session{}, err
	}
	defer unlock()
	return s.transition(id, from, to, reason)
}

func (s *Store) freshID() (string, error) {
	records, err := s.keyRecords()
	if err != nil {
		return "", err
	}
	reserved := make(map[string]struct{}, len(records))
	for _, record := range records {
		reserved[record.Session] = struct{}{}
	}
	for range idAttempts {
		id, err := NewID(s.pool)
		if err != nil {
			return "", err
		}
		if _, held := reserved[id]; held {
			continue
		}
		_, err = s.root.Lstat(fileName(id))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return id, nil
		case err != nil:
			return "", fmt.Errorf("check session ID: %w", err)
		}
	}
	return "", ErrExists
}

func (s *Store) createQueued(id string, now time.Time) (Session, error) {
	sess := Session{
		V: schemaVersion, ID: id, Pool: s.pool, State: StateQueued,
		Created: now, Updated: now,
	}
	data, err := encode(sess)
	if err != nil {
		return Session{}, err
	}
	if err := publish(s.root, fileName(id), data); err != nil {
		return Session{}, err
	}
	return sess, nil
}

func (s *Store) list() ([]Session, error) {
	list, err := entries(s.root)
	if err != nil {
		return nil, err
	}
	var out []Session
	for _, entry := range list {
		if tempPattern.MatchString(entry.Name()) {
			continue
		}
		id, ok := strings.CutSuffix(entry.Name(), fileExt)
		if !ok || !ValidID(id) {
			return nil, fmt.Errorf("%w: %q", errUnexpectedEntry, entry.Name())
		}
		sess, err := s.read(id)
		if err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	return out, nil
}

func (s *Store) transition(id string, from, to State, reason string) (Session, error) {
	if !CanTransition(from, to) {
		return Session{}, ErrIllegalTransition
	}
	cur, err := s.load(id)
	if err != nil {
		return Session{}, err
	}
	if cur.State != from {
		return Session{}, ErrStateConflict
	}
	next := cur
	next.State, next.Reason, next.Updated = to, reason, s.now().UTC()
	data, err := encode(next)
	if err != nil {
		return Session{}, err
	}
	if err := replace(s.root, fileName(id), data); err != nil {
		return Session{}, err
	}
	return next, nil
}

// hold takes the lock's mutex and checks both directories. The
// caller runs the returned function when its call is done.
func (s *Store) hold() (func(), error) {
	unlock, err := s.lock.hold()
	if err != nil {
		return nil, err
	}
	if !inPool(s.lock.root, s.root, sessionsName) || !inPool(s.lock.root, s.keys, keysName) {
		unlock()
		return nil, errStoreMoved
	}
	return unlock, nil
}

func (s *Store) load(id string) (Session, error) {
	if !ValidID(id) {
		return Session{}, errInvalidID
	}
	return s.read(id)
}

func fileName(id string) string { return id + fileExt }

func (s *Store) read(id string) (Session, error) {
	data, err := readRegular(s.root, fileName(id))
	if err != nil {
		return Session{}, err
	}
	sess, err := decode(data)
	if err != nil || sess.ID != id || sess.Pool != s.pool {
		return Session{}, fmt.Errorf("%w: %s", errInvalidSession, fileName(id))
	}
	return sess, nil
}
