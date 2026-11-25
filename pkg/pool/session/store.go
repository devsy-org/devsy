package session

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/pool/poolconfig"
)

const (
	fileExt      = ".json"
	tempPrefix   = ".tmp-"
	maxFileBytes = 64 << 10
	dirMode      = 0o700
	fileMode     = 0o600
	idAttempts   = 3
)

// Errors a caller can test with errors.Is.
var (
	ErrNotFound          = errors.New("session not found")
	ErrExists            = errors.New("session already exists")
	ErrStateConflict     = errors.New("session is not in the expected state")
	ErrIllegalTransition = errors.New("session state transition is not allowed")

	errUnexpectedEntry = errors.New("unexpected entry in the session directory")
	errLockNotHeld     = errors.New("the pool lock is not held")
	errForeignLock     = errors.New("the pool lock belongs to another pool directory")
	errNotRegular      = errors.New("session path is not a regular file")
	errTooLarge        = fmt.Errorf("session file is longer than %d bytes", maxFileBytes)
)

// renameFile is os.Rename, replaceable so a test can fail a publish.
var renameFile = os.Rename

// tempPattern matches the names stage creates with os.CreateTemp.
var tempPattern = regexp.MustCompile(`^\.tmp-[0-9]+$`)

// Store keeps one JSON file per session. Every write goes to a temp file in
// the same directory, is synced, and is then linked or renamed into place, so
// a reader sees the old file or the new one, never a partial one. A Store is
// bound to the held pool lock of its pool directory: only the lock holder can
// open one, every write checks that the lock is still held, and Release waits
// for a write in flight. That lock is the only thing that excludes other
// processes, and it also serializes the Stores opened on it in this process.
type Store struct {
	lock *Lock
	dir  string
	pool string
	now  func() time.Time
}

// OpenStore creates dir when needed, refuses one that other users can write
// to, and removes temp files a crashed writer left behind. dir must sit in the
// pool directory the lock covers. It refuses a nil, released or foreign lock
// before touching disk, and refuses on Windows the same way.
func OpenStore(dir, pool string, lock *Lock) (*Store, error) {
	if err := checkPlatform(); err != nil {
		return nil, err
	}
	if !poolconfig.ValidPoolName(pool) {
		return nil, errInvalidPool
	}
	if err := lock.covers(dir, pool); err != nil {
		return nil, err
	}
	unlock, err := lock.hold()
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := prepareDir(dir); err != nil {
		return nil, err
	}
	s := &Store{lock: lock, dir: dir, pool: pool, now: time.Now}
	if err := s.removeStrayTemps(); err != nil {
		return nil, err
	}
	return s, nil
}

func prepareDir(dir string) error {
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("create session directory: %w", err)
	}
	if err := checkDirMode(dir); err != nil {
		return err
	}
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("sync session directory: %w", err)
	}
	return nil
}

// Create writes a new queued session under a fresh ID.
func (s *Store) Create() (Session, error) {
	unlock, err := s.lock.hold()
	if err != nil {
		return Session{}, err
	}
	defer unlock()
	for range idAttempts {
		id, err := NewID(s.pool)
		if err != nil {
			return Session{}, err
		}
		now := s.now().UTC()
		sess := Session{
			V: schemaVersion, ID: id, Pool: s.pool, State: StateQueued,
			Created: now, Updated: now,
		}
		err = s.publish(sess)
		if !errors.Is(err, ErrExists) {
			return sess, err
		}
	}
	return Session{}, ErrExists
}

// Load reads one session.
func (s *Store) Load(id string) (Session, error) {
	if !ValidID(id) {
		return Session{}, errInvalidID
	}
	return s.read(id)
}

// List reads every session in ID order. A file that is not a valid session
// stops the listing: the pool cannot tell what state it holds.
func (s *Store) List() ([]Session, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("read session directory: %w", err)
	}
	var out []Session
	for _, entry := range entries {
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

// Transition moves a session from one state to another. It refuses an edge
// the state machine does not allow, and a session that is not in from. On any
// error the file on disk is unchanged, except that a failed directory sync
// after the rename may leave the new state on disk. A caller must treat an
// error as "state unknown" and reload, not retry blindly.
func (s *Store) Transition(id string, from, to State, reason string) (Session, error) {
	if !CanTransition(from, to) {
		return Session{}, ErrIllegalTransition
	}
	unlock, err := s.lock.hold()
	if err != nil {
		return Session{}, err
	}
	defer unlock()
	cur, err := s.Load(id)
	if err != nil {
		return Session{}, err
	}
	if cur.State != from {
		return Session{}, ErrStateConflict
	}
	next := cur
	next.State, next.Reason, next.Updated = to, reason, s.now().UTC()
	return next, s.replace(next)
}

// removeStrayTemps deletes temp files a killed writer left behind. The caller
// holds the lock, so no live writer owns one.
func (s *Store) removeStrayTemps() error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf("read session directory: %w", err)
	}
	for _, entry := range entries {
		if !tempPattern.MatchString(entry.Name()) || !entry.Type().IsRegular() {
			continue
		}
		err := os.Remove(filepath.Join(s.dir, entry.Name()))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove stray temp file: %w", err)
		}
	}
	return nil
}

func (s *Store) path(id string) string { return filepath.Join(s.dir, id+fileExt) }

func (s *Store) read(id string) (Session, error) {
	data, err := readRegular(s.path(id))
	if err != nil {
		return Session{}, err
	}
	sess, err := decode(data)
	if err != nil || sess.ID != id || sess.Pool != s.pool {
		return Session{}, fmt.Errorf("%w: %s", errInvalidSession, id+fileExt)
	}
	return sess, nil
}

// publish links a new file into place, which fails when the ID exists.
func (s *Store) publish(sess Session) error {
	tmp, err := s.stage(sess)
	if err != nil {
		return err
	}
	linkErr := os.Link(tmp, s.path(sess.ID))
	_ = os.Remove(tmp)
	if errors.Is(linkErr, fs.ErrExist) {
		return ErrExists
	}
	if linkErr != nil {
		return fmt.Errorf("publish session file: %w", linkErr)
	}
	return syncDir(s.dir)
}

func (s *Store) replace(sess Session) error {
	tmp, err := s.stage(sess)
	if err != nil {
		return err
	}
	if err := renameFile(tmp, s.path(sess.ID)); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("publish session file: %w", err)
	}
	return syncDir(s.dir)
}

// stage writes and syncs a temp file next to the final path.
func (s *Store) stage(sess Session) (string, error) {
	data, err := encode(sess)
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp(s.dir, tempPrefix+"*")
	if err != nil {
		return "", fmt.Errorf("create session temp file: %w", err)
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("write session temp file: %w", err)
	}
	return f.Name(), nil
}

// readRegular opens without following a link and refuses anything that is not
// a regular file within the size cap.
func readRegular(path string) ([]byte, error) {
	f, err := os.OpenFile(filepath.Clean(path), os.O_RDONLY|noFollow|nonBlock, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("open session file: %w", err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat session file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errNotRegular
	}
	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read session file: %w", err)
	}
	if len(data) > maxFileBytes {
		return nil, errTooLarge
	}
	return data, nil
}
