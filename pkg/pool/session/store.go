package session

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	errNotRegular      = errors.New("session path is not a regular file")
	errTooLarge        = fmt.Errorf("session file is longer than %d bytes", maxFileBytes)
)

// renameFile is os.Rename, replaceable so a test can fail a publish.
var renameFile = os.Rename

// Store keeps one JSON file per session. Every write goes to a temp file in
// the same directory, is synced, and is then linked or renamed into place, so
// a reader sees the old file or the new one, never a partial one. The store
// serializes calls in one process; the pool lock excludes other processes.
type Store struct {
	mu   sync.Mutex
	dir  string
	pool string
	now  func() time.Time
}

// OpenStore creates dir when needed and refuses one that other users can
// write to. It refuses on Windows before touching disk.
func OpenStore(dir, pool string) (*Store, error) {
	if err := checkPlatform(); err != nil {
		return nil, err
	}
	if !poolconfig.ValidPoolName(pool) {
		return nil, errInvalidPool
	}
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return nil, fmt.Errorf("create session directory: %w", err)
	}
	if err := checkDirMode(dir); err != nil {
		return nil, err
	}
	if err := syncDir(dir); err != nil {
		return nil, fmt.Errorf("sync session directory: %w", err)
	}
	return &Store{dir: dir, pool: pool, now: time.Now}, nil
}

// Create writes a new queued session under a fresh ID.
func (s *Store) Create() (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
		if strings.HasPrefix(entry.Name(), tempPrefix) {
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
// after the rename may leave the new state on disk.
func (s *Store) Transition(id string, from, to State, reason string) (Session, error) {
	if !CanTransition(from, to) {
		return Session{}, ErrIllegalTransition
	}
	s.mu.Lock()
	defer s.mu.Unlock()
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
	defer func() { _ = os.Remove(tmp) }()
	if err := os.Link(tmp, s.path(sess.ID)); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return ErrExists
		}
		return fmt.Errorf("publish session file: %w", err)
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
