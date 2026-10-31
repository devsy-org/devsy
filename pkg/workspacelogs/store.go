package workspacelogs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/gofrs/flock"
)

const (
	runsDirName  = "runs"
	producersDir = "producers"
	manifestName = "manifest.json"
	manifestLock = "manifest.lock"
	appendLock   = "append.lock"
	pruneLock    = "prune.lock"
	lockTimeout  = 15 * time.Second
	lockRetry    = 25 * time.Millisecond
	dirPerm      = 0o700
	filePerm     = 0o600
	maxNameBytes = 256
)

// Options configures a Store.
type Options struct {
	// Root is the absolute storage directory.
	Root string
	// Now overrides the clock in tests.
	Now func() time.Time
}

// Store is a file-backed archive of workspace run output. It is safe for use
// by several goroutines and several processes sharing one Root.
type Store struct {
	root string
	now  func() time.Time

	mu    sync.Mutex
	tails map[string]tail
}

// Open creates the storage root if needed and returns a Store. It also removes
// expired runs.
func Open(opts Options) (*Store, error) {
	if !filepath.IsAbs(opts.Root) {
		return nil, fmt.Errorf("%w: root must be absolute", ErrUnsafePath)
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if err := os.MkdirAll(opts.Root, dirPerm); err != nil {
		return nil, fmt.Errorf("create workspace logs root: %w", err)
	}
	s := &Store{root: opts.Root, now: opts.Now, tails: map[string]tail{}}
	_ = s.Prune()

	return s, nil
}

// OpenDefault opens the store at the PathManager workspace logs directory.
func OpenDefault() (*Store, error) {
	root, err := config.DefaultPathManager().WorkspaceLogsDir()
	if err != nil {
		return nil, err
	}

	return Open(Options{Root: root})
}

// CreateRun creates a run, or joins the run named by spec.RunID. Joining
// requires the stored context (and workspace UID, when both are set) to match.
func (s *Store) CreateRun(spec RunSpec) (*RunManifest, error) {
	if err := validateSpec(spec); err != nil {
		return nil, err
	}
	if spec.RunID == "" {
		spec.RunID = NewID()
	}
	dir, err := s.runDir(spec.RunID)
	if err != nil {
		return nil, err
	}
	if err = s.ensureDir(runsDirName, spec.RunID); err != nil {
		return nil, err
	}
	var created *RunManifest
	err = withLock(filepath.Join(dir, manifestLock), func() error {
		var lockErr error
		created, lockErr = s.createOrJoin(dir, spec)

		return lockErr
	})
	if err != nil {
		return nil, err
	}

	return created, nil
}

func joinConflict(m *RunManifest, spec RunSpec) error {
	if m.Context != spec.Context {
		return fmt.Errorf("%w: run belongs to another context", ErrIdentityConflict)
	}
	if m.WorkspaceUID != nil && spec.WorkspaceUID != "" && *m.WorkspaceUID != spec.WorkspaceUID {
		return fmt.Errorf("%w: run belongs to another workspace", ErrIdentityConflict)
	}

	return nil
}

func validateSpec(spec RunSpec) error {
	if spec.RunID != "" {
		if err := requireID("runId", spec.RunID); err != nil {
			return err
		}
	}
	for name, v := range map[string]string{
		"context": spec.Context, "workspaceUid": spec.WorkspaceUID,
		"workspaceId": spec.WorkspaceIDAtStart, "command": spec.Command, "taskId": spec.TaskID,
	} {
		if len(v) > maxNameBytes {
			return fmt.Errorf("%w: %s is too long", ErrInvalidRecord, name)
		}
	}
	if spec.Context == "" || spec.Command == "" {
		return fmt.Errorf("%w: context and command are required", ErrInvalidRecord)
	}

	return nil
}

// Manifest returns a copy of the run manifest.
func (s *Store) Manifest(runID string) (*RunManifest, error) {
	dir, err := s.runDir(runID)
	if err != nil {
		return nil, err
	}

	return s.readManifest(dir)
}

// BindWorkspace binds the resolved workspace UID to a provisional run. Binding
// the same UID again is a no-op; a different UID is rejected.
func (s *Store) BindWorkspace(runID, uid, idAtStart string) error {
	if uid == "" || len(uid) > maxNameBytes {
		return fmt.Errorf("%w: workspace UID", ErrInvalidRecord)
	}

	return s.update(runID, func(m *RunManifest) error {
		if m.WorkspaceUID != nil {
			if *m.WorkspaceUID != uid {
				return ErrIdentityConflict
			}

			return nil
		}
		m.WorkspaceUID = &uid
		if idAtStart != "" && len(idAtStart) <= maxNameBytes {
			m.WorkspaceIDAtStart = idAtStart
		}

		return nil
	})
}

// SetCommandState records the originating command's outcome.
func (s *Store) SetCommandState(runID string, state CommandState) error {
	return s.update(runID, func(m *RunManifest) error {
		m.CommandState = state

		return nil
	})
}

// RegisterProducer reserves a producer in the run. Registering a known
// producer is a no-op. Overflowing MaxProducers marks capture partial and
// returns ErrProducerCap; execution is not affected.
func (s *Store) RegisterProducer(runID string, ref ProducerRef) error {
	if err := requireID("producerId", ref.ProducerID); err != nil {
		return err
	}
	if ref.State == "" {
		ref.State = ProducerReserved
	}
	var capErr error

	err := s.update(runID, func(m *RunManifest) error {
		if m.producer(ref.ProducerID) >= 0 {
			return nil
		}
		if m.DiscoverySealed {
			return ErrDiscoverySealed
		}
		if len(m.Producers) >= MaxProducers {
			m.addCondition(ConditionProducerCap)
			capErr = ErrProducerCap

			return nil
		}
		m.Producers = append(m.Producers, ref)
		if m.CaptureState == CapturePending {
			m.CaptureState = CaptureRecording
		}

		return nil
	})
	if err != nil {
		return err
	}

	return capErr
}

// SealDiscovery closes producer discovery. Call it after every scheduler has
// recorded its child reservations or exclusions.
func (s *Store) SealDiscovery(runID string) error {
	return s.update(runID, func(m *RunManifest) error {
		m.DiscoverySealed = true

		return nil
	})
}

// AddCondition records a capture condition and marks capture partial.
func (s *Store) AddCondition(runID string, c Condition) error {
	return s.update(runID, func(m *RunManifest) error {
		m.addCondition(c)

		return nil
	})
}

func (m *RunManifest) addCondition(c Condition) {
	if !slices.Contains(m.Conditions, c) {
		m.Conditions = append(m.Conditions, c)
	}
	m.CaptureState = CapturePartial
}

func (m *RunManifest) producer(id string) int {
	return slices.IndexFunc(m.Producers, func(p ProducerRef) bool { return p.ProducerID == id })
}

// refreshCapture settles a clean run to complete once nothing more can arrive.
func (m *RunManifest) refreshCapture() {
	if m.CaptureState == CaptureRecording && !m.Active() {
		m.CaptureState = CaptureComplete
	}
}

func (s *Store) createOrJoin(dir string, spec RunSpec) (*RunManifest, error) {
	existing, err := s.readManifest(dir)
	if err == nil {
		return existing, joinConflict(existing, spec)
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	now := s.now().UTC()
	m := &RunManifest{
		SchemaVersion:      SchemaVersion,
		RunID:              spec.RunID,
		Context:            spec.Context,
		WorkspaceIDAtStart: spec.WorkspaceIDAtStart,
		Command:            spec.Command,
		TaskID:             spec.TaskID,
		CreatedAt:          now,
		UpdatedAt:          now,
		CommandState:       CommandRunning,
		CaptureState:       CapturePending,
	}
	if spec.WorkspaceUID != "" {
		m.WorkspaceUID = &spec.WorkspaceUID
	}
	if s.storeFull() {
		m.addCondition(ConditionCapacity)
	}

	return m, s.writeManifest(dir, m)
}

func (s *Store) update(runID string, fn func(*RunManifest) error) error {
	dir, err := s.runDir(runID)
	if err != nil {
		return err
	}

	return withLock(filepath.Join(dir, manifestLock), func() error {
		m, readErr := s.readManifest(dir)
		if readErr != nil {
			return readErr
		}
		if fnErr := fn(m); fnErr != nil {
			return fnErr
		}
		m.refreshCapture()
		m.UpdatedAt = s.now().UTC()

		return s.writeManifest(dir, m)
	})
}

func (s *Store) readManifest(dir string) (*RunManifest, error) {
	path := filepath.Join(dir, manifestName)
	if err := checkRegular(path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path) //nolint:gosec // path is built from a validated run ID
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read run manifest: %w", err)
	}
	var m RunManifest
	if err = json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("decode run manifest: %w", err)
	}

	return &m, nil
}

func (s *Store) writeManifest(dir string, m *RunManifest) error {
	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("encode run manifest: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "manifest-*.tmp")
	if err != nil {
		return fmt.Errorf("create manifest temp file: %w", err)
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if err = errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmp.Name())

		return fmt.Errorf("write run manifest: %w", err)
	}
	if err = os.Rename(tmp.Name(), filepath.Join(dir, manifestName)); err != nil {
		_ = os.Remove(tmp.Name())

		return fmt.Errorf("replace run manifest: %w", err)
	}

	return nil
}

func (s *Store) runDir(runID string) (string, error) {
	if err := requireID("runId", runID); err != nil {
		return "", err
	}

	return filepath.Join(s.root, runsDirName, runID), nil
}

func (s *Store) producerDir(runID, producerID string) (string, error) {
	dir, err := s.runDir(runID)
	if err != nil {
		return "", err
	}
	if err = requireID("producerId", producerID); err != nil {
		return "", err
	}

	return filepath.Join(dir, producersDir, producerID), nil
}

// ensureDir creates each component beneath the root and rejects symlinks and
// non-directories. Components are validated IDs or fixed names, never raw
// caller selectors.
func (s *Store) ensureDir(parts ...string) error {
	cur := s.root
	for _, part := range parts {
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if errors.Is(err, fs.ErrNotExist) {
			if err = os.Mkdir(cur, dirPerm); err != nil && !errors.Is(err, fs.ErrExist) {
				return fmt.Errorf("create %s: %w", cur, err)
			}

			continue
		}
		if err != nil {
			return fmt.Errorf("stat %s: %w", cur, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("%w: %s is not a directory", ErrUnsafePath, cur)
		}
	}

	return nil
}

// checkRegular rejects a path that exists but is a symlink or not a regular
// file. A missing path is fine.
func checkRegular(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: %s is not a regular file", ErrUnsafePath, path)
	}

	return nil
}

func withLock(path string, fn func() error) error {
	l := flock.New(path, flock.SetPermissions(filePerm))
	ctx, cancel := context.WithTimeout(context.Background(), lockTimeout)
	defer cancel()

	locked, err := l.TryLockContext(ctx, lockRetry)
	if err != nil {
		return fmt.Errorf("lock %s: %w", filepath.Base(path), err)
	}
	if !locked {
		return fmt.Errorf("lock %s: timed out", filepath.Base(path))
	}
	defer func() { _ = l.Unlock() }()

	return fn()
}
