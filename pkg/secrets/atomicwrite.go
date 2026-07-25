package secrets

import (
	"fmt"
	"os"
	"path/filepath"
)

// atomicWriteFile writes via a same-dir temp file then rename, so a crash
// mid-write cannot leave a truncated store file.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create secrets dir: %w", err)
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp secrets file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp secrets file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp secrets file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp secrets file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp secrets file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace secrets file: %w", err)
	}
	syncDir(dir)

	return nil
}

// syncDir fsyncs a directory so a rename is durable across a crash. Best-effort:
// some filesystems reject directory sync, and durability is not correctness.
func syncDir(dir string) {
	d, err := os.Open(dir) // #nosec G304 -- secrets config dir.
	if err != nil {
		return
	}
	defer func() { _ = d.Close() }()
	_ = d.Sync()
}
