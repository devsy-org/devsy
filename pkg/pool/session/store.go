package session

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/pool/poolconfig"
)

const (
	fileExt      = ".json"
	sessionsName = "sessions"
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

	errUnexpectedEntry = errors.New("unexpected entry in the session directory")
	errLockNotHeld     = errors.New("the pool lock is not held")
	errForeignLock     = errors.New("the pool lock belongs to another pool directory")
	errNotDirectory    = errors.New("session path is not a directory")
	errNotRegular      = errors.New("session path is not a regular file")
	errTooLarge        = fmt.Errorf("session file is longer than %d bytes", maxFileBytes)
)

// renameFile is Root.Rename, replaceable so a test can fail or pause a
// publish.
var renameFile = func(root *os.Root, oldName, newName string) error {
	return root.Rename(oldName, newName)
}

// tempPattern matches the names stage gives its temp files.
var tempPattern = regexp.MustCompile(`^\.tmp-[0-9]+$`)

// Store keeps one JSON file per session. Every write goes to a temp file in
// the same directory, is synced, and is then linked or renamed into place, so
// a reader sees the old file or the new one, never a partial one. A Store is
// bound to the held pool lock: only the lock holder can open one, it works
// through the session directory that was open when the lock was checked, every
// write checks that the lock is still held and still names the same lock file,
// and Release waits for a write in flight. That lock is the only thing that
// excludes other processes, and it also serializes the Stores opened on it in
// this process.
type Store struct {
	lock *Lock
	root *os.Root
	pool string
	now  func() time.Time
}

// OpenStore opens the session directory of the pool the lock covers, creating
// it when needed, and removes temp files a crashed writer left behind. dir
// must be <pool directory>/sessions for the locked pool directory and must
// not be a symlink. It refuses a nil, released, replaced or foreign lock
// before changing anything on disk, and refuses on Windows the same way. The
// caller closes the Store.
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
	root, err := openSessionsRoot(lock.root)
	if err != nil {
		return nil, err
	}
	s := &Store{lock: lock, root: root, pool: pool, now: time.Now}
	if err := s.removeStrayTemps(); err != nil {
		_ = root.Close()
		return nil, err
	}
	return s, nil
}

// openSessionsRoot opens the sessions directory inside the pool directory. A
// symlink there is refused, so a link cannot send the store into another pool.
func openSessionsRoot(pool *os.Root) (*os.Root, error) {
	info, err := pool.Lstat(sessionsName)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := pool.Mkdir(sessionsName, dirMode); err != nil {
			return nil, fmt.Errorf("create session directory: %w", err)
		}
		if err := syncRoot(pool); err != nil {
			return nil, fmt.Errorf("sync pool directory: %w", err)
		}
	case err != nil:
		return nil, fmt.Errorf("stat session directory: %w", err)
	case !info.IsDir():
		return nil, errNotDirectory
	}
	root, err := pool.OpenRoot(sessionsName)
	if err != nil {
		return nil, fmt.Errorf("open session directory: %w", err)
	}
	if err := checkRoot(root); err != nil {
		_ = root.Close()
		return nil, err
	}
	return root, nil
}

// Close releases the session directory. It does not release the lock.
func (s *Store) Close() error { return s.root.Close() }

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
	entries, err := s.entries()
	if err != nil {
		return nil, err
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

// entries lists the session directory in name order.
func (s *Store) entries() ([]fs.DirEntry, error) {
	dir, err := s.root.Open(".")
	if err != nil {
		return nil, fmt.Errorf("open session directory: %w", err)
	}
	defer func() { _ = dir.Close() }()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, fmt.Errorf("read session directory: %w", err)
	}
	slices.SortFunc(
		entries,
		func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) },
	)
	return entries, nil
}

// removeStrayTemps deletes temp files a killed writer left behind. The caller
// holds the lock, so no live writer owns one.
func (s *Store) removeStrayTemps() error {
	entries, err := s.entries()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !tempPattern.MatchString(entry.Name()) || !entry.Type().IsRegular() {
			continue
		}
		err := s.root.Remove(entry.Name())
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove stray temp file: %w", err)
		}
	}
	return nil
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

// publish links a new file into place, which fails when the ID exists.
func (s *Store) publish(sess Session) error {
	tmp, err := s.stage(sess)
	if err != nil {
		return err
	}
	linkErr := s.root.Link(tmp, fileName(sess.ID))
	_ = s.root.Remove(tmp)
	if errors.Is(linkErr, fs.ErrExist) {
		return ErrExists
	}
	if linkErr != nil {
		return fmt.Errorf("publish session file: %w", linkErr)
	}
	return syncRoot(s.root)
}

func (s *Store) replace(sess Session) error {
	tmp, err := s.stage(sess)
	if err != nil {
		return err
	}
	if err := renameFile(s.root, tmp, fileName(sess.ID)); err != nil {
		_ = s.root.Remove(tmp)
		return fmt.Errorf("publish session file: %w", err)
	}
	return syncRoot(s.root)
}

// stage writes and syncs a temp file next to the final path.
func (s *Store) stage(sess Session) (string, error) {
	data, err := encode(sess)
	if err != nil {
		return "", err
	}
	f, name, err := s.createTemp()
	if err != nil {
		return "", err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = s.root.Remove(name)
		return "", fmt.Errorf("write session temp file: %w", err)
	}
	return name, nil
}

// createTemp makes a new file named like tempPattern and returns its name
// inside the session directory. It never reuses a name.
func (s *Store) createTemp() (*os.File, string, error) {
	for range tempAttempts {
		var raw [4]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return nil, "", fmt.Errorf("name session temp file: %w", err)
		}
		name := tempPrefix + strconv.FormatUint(uint64(binary.BigEndian.Uint32(raw[:])), 10)
		f, err := s.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return nil, "", fmt.Errorf("create session temp file: %w", err)
		}
		return f, name, nil
	}
	return nil, "", fmt.Errorf("create session temp file: %w", fs.ErrExist)
}

// isRegularFile reports whether name is a regular file and the open file is
// that same file, not something a link led to.
func isRegularFile(root *os.Root, name string, opened fs.FileInfo) bool {
	link, err := root.Lstat(name)
	return err == nil && link.Mode().IsRegular() && opened.Mode().IsRegular() &&
		os.SameFile(link, opened)
}

// readRegular refuses a link, which Root follows inside the directory, and
// anything else that is not a regular file within the size cap.
func readRegular(root *os.Root, name string) ([]byte, error) {
	f, err := root.OpenFile(name, os.O_RDONLY|nonBlock, 0)
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
	if !isRegularFile(root, name, info) {
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
