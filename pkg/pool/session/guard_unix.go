//go:build !windows

package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

const (
	noFollow = syscall.O_NOFOLLOW
	// nonBlock keeps an open from hanging on a FIFO planted at a path; the
	// regular-file check after the open then refuses it.
	nonBlock = syscall.O_NONBLOCK
)

func checkPlatform() error { return nil }

func checkDirMode(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("stat pool directory: %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("pool directory must be accessible only by its owner: %s", dir)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("pool directory must be owned by the current user: %s", dir)
	}
	return nil
}

// syncDir persists the entries created or renamed in dir. A filesystem that
// cannot fsync a directory has no stronger guarantee to wait for.
func syncDir(dir string) error {
	d, err := os.Open(filepath.Clean(dir))
	if err != nil {
		return err
	}
	err = d.Sync()
	if errors.Is(err, syscall.EINVAL) ||
		errors.Is(err, syscall.ENOTSUP) ||
		errors.Is(err, syscall.EOPNOTSUPP) {
		err = nil
	}
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	return err
}
