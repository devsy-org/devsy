//go:build !windows

package provider

import "os"

// syncDir fsyncs a directory so a prior rename(2) into it survives a crash,
// not just becomes visible to concurrent readers.
func syncDir(dir string) error {
	//nolint:gosec // dir is controlled, derived from WriteFileAtomic's path argument
	f, err := os.OpenFile(dir, os.O_RDONLY, os.ModePerm)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}
