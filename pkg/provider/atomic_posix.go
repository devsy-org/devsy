//go:build !windows

package provider

import "os"

// syncDir fsyncs a directory so that a prior rename(2) into it is durable
// across a crash, not just visible to concurrent readers. Directory fsync
// has no reliable equivalent on Windows (NTFS handles directory metadata
// durability differently and os.File.Sync on a directory handle is not
// meaningful there), so this is POSIX-only; atomic_windows.go provides a
// no-op for the same signature.
func syncDir(dir string) error {
	//nolint:gosec // dir is controlled, derived from WriteFileAtomic's path argument
	f, err := os.OpenFile(dir, os.O_RDONLY, os.ModePerm)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}
