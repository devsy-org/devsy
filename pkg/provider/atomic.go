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
// This helper does NOT guarantee crash durability: it syncs the temp file
// but not the parent directory, so a power loss between rename(2)
// returning and the directory entry being flushed could lose the rename.
// That tradeoff is acceptable for the config files this is used for
// (callers retry / re-resolve on the next run). If you need crash-safe
// persistence for a new caller, add a parent-directory fsync here and
// audit existing callers for the latency cost.
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
	return nil
}
