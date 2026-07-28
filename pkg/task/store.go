package task

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/random"
	"github.com/gofrs/flock"
)

// lockTimeout bounds how long update waits to acquire a task's file lock.
// Held only for the duration of a single read-modify-write, so a stuck lock
// past this means something is wrong rather than merely busy.
const lockTimeout = 5 * time.Second

// Store persists task state as one JSON file per task under dir. update
// serializes read-modify-write across processes with a file lock, since a
// task's worker (reporting progress) and an external `task cancel` both
// write to the same file.
type Store struct {
	dir string
}

func NewStore() (*Store, error) {
	dir, err := config.DefaultPathManager().TaskDir()
	if err != nil {
		return nil, fmt.Errorf("task dir: %w", err)
	}
	return NewStoreAt(dir)
}

func NewStoreAt(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create task dir: %w", err)
	}
	return &Store{dir: dir}, nil
}

func (s *Store) Create(opts CreateOptions) (*Task, error) {
	id := random.String(12)
	now := time.Now()
	state := &State{
		ID:          id,
		Command:     opts.Command,
		WorkspaceID: opts.WorkspaceID,
		Status:      StatusPending,
		StartedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.write(state); err != nil {
		return nil, err
	}
	return &Task{store: s, id: id}, nil
}

// Open returns a handle without reading the task's state.
func (s *Store) Open(id string) *Task {
	return &Task{store: s, id: id}
}

// Delete errors if the task is still pending or running unless force is set.
func (s *Store) Delete(id string, force bool) error {
	if !force {
		state, err := s.Get(id)
		if err != nil {
			return err
		}
		if !state.Status.Terminal() {
			return fmt.Errorf(
				"task %s is still %s; cancel it first or delete with force",
				id, state.Status,
			)
		}
	}
	path, err := s.path(id)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("delete task %s: %w", id, err)
	}
	return nil
}

func (s *Store) Get(id string) (*State, error) {
	path, err := s.path(id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path) // #nosec G304 -- path validated by s.path
	if err != nil {
		return nil, fmt.Errorf("read task %s: %w", id, err)
	}
	state := &State{}
	if err := json.Unmarshal(data, state); err != nil {
		return nil, fmt.Errorf("parse task %s: %w", id, err)
	}
	return state, nil
}

// List returns every known task, most recently started first.
func (s *Store) List() ([]*State, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("read task dir: %w", err)
	}

	states := make([]*State, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := entry.Name()[:len(entry.Name())-len(".json")]
		state, err := s.Get(id)
		if err != nil {
			continue
		}
		states = append(states, state)
	}

	sort.Slice(states, func(i, j int) bool {
		return states[i].StartedAt.After(states[j].StartedAt)
	})
	return states, nil
}

func (s *Store) update(id string, mutate func(*State)) error {
	return s.withLock(id, func() error {
		state, err := s.Get(id)
		if err != nil {
			return err
		}
		mutate(state)
		state.touch()
		return s.write(state)
	})
}

// withLock runs fn holding an OS file lock scoped to id, so a concurrent
// read-modify-write from another process (or another Store instance in this
// one) can't interleave with it.
func (s *Store) withLock(id string, fn func() error) error {
	path, err := s.path(id)
	if err != nil {
		return err
	}

	lock := flock.New(path + ".lock")
	ctx, cancel := context.WithTimeout(context.Background(), lockTimeout)
	defer cancel()
	locked, err := lock.TryLockContext(ctx, 20*time.Millisecond)
	if err != nil {
		return fmt.Errorf("lock task %s: %w", id, err)
	}
	if !locked {
		return fmt.Errorf("lock task %s: timed out", id)
	}
	defer func() { _ = lock.Unlock() }()

	return fn()
}

// path rejects any id that isn't a single clean path component, so a
// caller-supplied id like "../../etc/passwd" can't escape s.dir.
func (s *Store) path(id string) (string, error) {
	if id == "" || id == "." || id == ".." || filepath.Base(id) != id {
		return "", fmt.Errorf("invalid task id %q", id)
	}
	return filepath.Join(s.dir, id+".json"), nil
}

// write atomically replaces the task's state file so a concurrent Get never
// observes a partially written file.
func (s *Store) write(state *State) error {
	data, err := marshalState(state)
	if err != nil {
		return err
	}

	target, err := s.path(state.ID)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, state.ID+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp task file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write task state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp task file: %w", err)
	}
	if err := os.Rename(tmpPath, target); err != nil {
		return fmt.Errorf("commit task state: %w", err)
	}
	return nil
}
