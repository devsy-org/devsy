package workspacelogs

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/gofrs/flock"
)

// Retention bounds. They count encoded bytes on disk, not text length.
const (
	// MaxSegmentBytes is the size at which a producer rolls to a new segment.
	MaxSegmentBytes = 512 * 1024
	// MaxRunBytes is the retained data per run in one physical store.
	MaxRunBytes = 10 * 1024 * 1024
	// MaxGenerationBytes is the retained data per workspace generation.
	MaxGenerationBytes = 50 * 1024 * 1024
	// MaxCompletedRuns is the completed runs kept per workspace generation.
	MaxCompletedRuns = 20
	// RetentionAge is how long a run is kept after its last update.
	RetentionAge = 30 * 24 * time.Hour
	// MaxStoreBytes is the total retained data per installation.
	MaxStoreBytes = 512 * 1024 * 1024

	// metadataReserve is the part of a run's budget only metadata may use, so a
	// full output budget cannot hide the terminal state of a hook.
	metadataReserve = 256 * 1024
)

type runInfo struct {
	dir      string
	manifest *RunManifest
	bytes    int64
}

// generation is the retention grouping: one workspace UID inside a context.
// Runs not yet bound to a UID form their own per-context group.
func (r runInfo) generation() string {
	uid := ""
	if r.manifest.WorkspaceUID != nil {
		uid = *r.manifest.WorkspaceUID
	}

	return r.manifest.Context + "\x00" + uid
}

// Prune removes expired and surplus completed runs. It never removes a run
// with an active writer, unless the run has not been updated within
// RetentionAge (a crashed writer). Another process pruning at the same time
// makes this a no-op.
func (s *Store) Prune() error {
	l := flock.New(filepath.Join(s.root, pruneLock), flock.SetPermissions(filePerm))
	locked, err := l.TryLock()
	if err != nil || !locked {
		return err
	}
	defer func() { _ = l.Unlock() }()

	runs := s.scanRuns()
	runs = s.pruneExpired(runs)
	runs = s.pruneGenerations(runs)
	s.pruneTotal(runs)

	return nil
}

func (s *Store) scanRuns() []runInfo {
	runs := s.loadRuns()
	for i := range runs {
		runs[i].bytes = dirBytes(runs[i].dir)
	}

	return runs
}

// loadRuns reads every valid run manifest, newest first. Directories that are
// not run IDs or have no readable manifest are skipped.
func (s *Store) loadRuns() []runInfo {
	entries, err := os.ReadDir(filepath.Join(s.root, runsDirName))
	if err != nil {
		return nil
	}
	var runs []runInfo
	for _, e := range entries {
		if !e.IsDir() || !ValidID(e.Name()) {
			continue
		}
		dir := filepath.Join(s.root, runsDirName, e.Name())
		m, readErr := s.readManifest(dir)
		if readErr != nil {
			continue
		}
		runs = append(runs, runInfo{dir: dir, manifest: m})
	}
	slices.SortFunc(
		runs,
		func(a, b runInfo) int { return b.manifest.CreatedAt.Compare(a.manifest.CreatedAt) },
	)

	return runs
}

// removable reports whether the run may be deleted now.
func (s *Store) removable(r runInfo) bool {
	return !r.manifest.Active() || s.expired(r)
}

func (s *Store) expired(r runInfo) bool {
	return s.now().Sub(r.manifest.UpdatedAt) > RetentionAge
}

func (s *Store) pruneExpired(runs []runInfo) []runInfo {
	return slices.DeleteFunc(runs, func(r runInfo) bool {
		return s.expired(r) && s.remove(r)
	})
}

// pruneGenerations keeps the newest completed runs of each generation within
// the count and byte caps. Runs arrive newest first.
func (s *Store) pruneGenerations(runs []runInfo) []runInfo {
	count := map[string]int{}
	size := map[string]int64{}

	return slices.DeleteFunc(runs, func(r runInfo) bool {
		key := r.generation()
		size[key] += r.bytes
		if r.manifest.Active() {
			return false
		}
		count[key]++

		return (count[key] > MaxCompletedRuns || size[key] > MaxGenerationBytes) && s.remove(r)
	})
}

// pruneTotal removes the oldest removable runs until the store fits.
func (s *Store) pruneTotal(runs []runInfo) {
	var total int64
	for _, r := range runs {
		total += r.bytes
	}
	for i := len(runs) - 1; i >= 0 && total > MaxStoreBytes; i-- {
		if s.removable(runs[i]) && s.remove(runs[i]) {
			total -= runs[i].bytes
		}
	}
}

func (s *Store) remove(r runInfo) bool {
	info, err := os.Lstat(r.dir)
	if err != nil || !info.IsDir() {
		return false
	}

	return os.RemoveAll(r.dir) == nil
}

// storeFull reports whether the store has reached its total byte cap, so a new
// run starts with capacity already exhausted.
func (s *Store) storeFull() bool {
	return dirBytes(filepath.Join(s.root, runsDirName)) >= MaxStoreBytes
}

func dirBytes(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil //nolint:nilerr // best effort: unreadable entries do not count
		}
		if info, infoErr := d.Info(); infoErr == nil {
			total += info.Size()
		}

		return nil
	})

	return total
}
