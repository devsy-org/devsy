package feature

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
)

// LockedFeature is a single pinned entry in a devcontainer-lock.json file. It
// mirrors the structure produced by the reference devcontainer CLI.
type LockedFeature struct {
	Version   string         `json:"version,omitempty"`
	Resolved  string         `json:"resolved,omitempty"`
	Integrity string         `json:"integrity,omitempty"`
	DependsOn map[string]any `json:"dependsOn,omitempty"`
}

// Lockfile mirrors the devcontainer-lock.json structure: a map of feature
// identifier to its pinned resolution.
type Lockfile struct {
	Features map[string]LockedFeature `json:"features"`
}

var (
	errLockfileMissing  = errors.New("lockfile does not exist")
	errLockfileMismatch = errors.New("lockfile does not match")
)

// LockfilePath returns the lockfile path for a devcontainer config origin,
// matching the reference CLI: a hidden config file (basename starting with a
// dot) yields a hidden lockfile, otherwise devcontainer-lock.json is used.
// It returns "" when the origin is unknown or not a local path.
func LockfilePath(configOrigin string) string {
	if configOrigin == "" || strings.Contains(configOrigin, "://") {
		return ""
	}
	name := "devcontainer-lock.json"
	if strings.HasPrefix(filepath.Base(configOrigin), ".") {
		name = ".devcontainer-lock.json"
	}
	return filepath.Join(filepath.Dir(configOrigin), name)
}

// ReadLockfile loads the lockfile at path. A missing or empty file yields an
// empty lockfile rather than an error.
func ReadLockfile(path string) (*Lockfile, error) {
	lf := &Lockfile{Features: map[string]LockedFeature{}}
	if path == "" {
		return lf, nil
	}

	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return lf, nil
		}
		return nil, fmt.Errorf("read lockfile %s: %w", path, err)
	}

	if len(bytes.TrimSpace(data)) == 0 {
		return lf, nil
	}

	if err := json.Unmarshal(data, lf); err != nil {
		return nil, fmt.Errorf("parse lockfile %s: %w", path, err)
	}
	if lf.Features == nil {
		lf.Features = map[string]LockedFeature{}
	}
	return lf, nil
}

// WriteLockfile persists the lockfile at path, matching reference-CLI
// semantics: it only rewrites when the normalized content changed, and when
// frozen it never writes, instead erroring if the file is missing or stale.
func WriteLockfile(path string, lf *Lockfile, frozen bool) error {
	if path == "" {
		return nil
	}

	newContent, err := marshalLockfile(lf)
	if err != nil {
		return err
	}

	write, err := lockfileNeedsWrite(path, newContent, frozen)
	if err != nil || !write {
		return err
	}

	if err := os.WriteFile(path, newContent, 0o600); err != nil {
		return fmt.Errorf("write lockfile %s: %w", path, err)
	}
	log.Debugf("wrote devcontainer lockfile: %s", path)
	return nil
}

// lockfileNeedsWrite reports whether the lockfile at path must be rewritten,
// applying frozen-mode enforcement (error if missing or stale).
func lockfileNeedsWrite(path string, newContent []byte, frozen bool) (bool, error) {
	existing, err := os.ReadFile(filepath.Clean(path))
	switch {
	case err == nil:
		if bytes.Equal(normalizeLockfileJSON(existing), normalizeLockfileJSON(newContent)) {
			return false, nil
		}
		if frozen {
			return false, errLockfileMismatch
		}
		return true, nil
	case errors.Is(err, os.ErrNotExist):
		if frozen {
			return false, errLockfileMissing
		}
		return true, nil
	default:
		return false, fmt.Errorf("read lockfile %s: %w", path, err)
	}
}

// marshalLockfile renders the lockfile with two-space indentation and a
// trailing newline, matching the reference CLI output.
func marshalLockfile(lf *Lockfile) ([]byte, error) {
	data, err := json.MarshalIndent(lf, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal lockfile: %w", err)
	}
	return append(data, '\n'), nil
}

// normalizeLockfileJSON round-trips JSON so cosmetic differences (indentation,
// trailing whitespace) do not trigger a rewrite. Invalid input normalizes to
// nil, which forces a rewrite.
func normalizeLockfileJSON(data []byte) []byte {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil
	}
	out, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return out
}

// lockfileMode controls how the feature lockfile participates in a fetch.
type lockfileMode struct {
	// write creates or updates the lockfile after resolution.
	write bool
	// frozen fails the build instead of writing when the lockfile is missing or
	// would change.
	frozen bool
	// disabled turns the lockfile off entirely — no load, no pinning, no write
	// (the --no-lockfile opt-out).
	disabled bool
}

// lockfileState carries the lockfile loaded for pinning and collects the
// entries resolved during a fetch so they can be written afterwards.
type lockfileState struct {
	loaded  *Lockfile
	entries map[string]LockedFeature
}

// pin returns the resolved reference and integrity digest recorded for a
// feature identifier, if the loaded lockfile contains it.
func (l *lockfileState) pin(featureID string) (resolved, integrity string, ok bool) {
	if l == nil || l.loaded == nil {
		return "", "", false
	}
	entry, found := l.loaded.Features[featureID]
	if !found {
		return "", "", false
	}
	return entry.Resolved, entry.Integrity, true
}

// record stores the resolved entry for a feature identifier.
func (l *lockfileState) record(featureID string, entry LockedFeature) {
	if l == nil {
		return
	}
	l.entries[featureID] = entry
}

// newLockfileState loads the lockfile for the given config to enable pinning.
// It always returns a usable state; a missing lockfile simply yields no pins.
func newLockfileState(cfg *config.DevContainerConfig) *lockfileState {
	state := &lockfileState{entries: map[string]LockedFeature{}}
	loaded, err := ReadLockfile(LockfilePath(cfg.Origin))
	if err != nil {
		log.Warnf("ignoring unreadable devcontainer lockfile: %v", err)
		return state
	}
	state.loaded = loaded
	return state
}

// checkFrozenPrecondition fails fast in frozen mode when the config declares
// features but no lockfile exists, avoiding wasted feature downloads before the
// commit-time enforcement would reject the build anyway.
func (l *lockfileState) checkFrozenPrecondition(cfg *config.DevContainerConfig) error {
	if l == nil || len(cfg.Features) == 0 {
		return nil
	}
	path := LockfilePath(cfg.Origin)
	if path == "" || fileExists(path) {
		return nil
	}
	return errLockfileMissing
}

// commit writes the collected entries to the lockfile when write is requested.
// It regenerates the lockfile fully from the freshly resolved entries so that
// removed features drop out. To avoid creating spurious empty lockfiles, it
// skips writing when there are no entries and no lockfile already exists.
func (l *lockfileState) commit(cfg *config.DevContainerConfig, mode lockfileMode) error {
	if l == nil || !mode.write {
		return nil
	}

	path := LockfilePath(cfg.Origin)
	if path == "" {
		return nil
	}

	if len(l.entries) == 0 && !fileExists(path) {
		return nil
	}

	return WriteLockfile(path, &Lockfile{Features: l.entries}, mode.frozen)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
