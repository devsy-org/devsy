package provider

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteFileAtomic writes data to path atomically by writing to a sibling
// temp file then renaming. POSIX rename(2) ensures concurrent readers see
// either the old or the new content, never a partially-written file —
// which is the guarantee callers of this helper rely on for config files
// like workspace.json.
//
// On POSIX platforms this also fsyncs the parent directory after the
// rename, so the rename itself survives a crash immediately after this
// function returns (see atomic_posix.go). On Windows the directory sync is
// a no-op (see atomic_windows.go) — Windows callers retain the pre-existing
// weaker guarantee and should still tolerate re-resolving state on the next
// run, as they always have.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("rename temp file: %w", err)
	}
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("sync parent dir: %w", err)
	}
	return nil
}
