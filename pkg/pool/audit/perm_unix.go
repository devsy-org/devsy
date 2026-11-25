//go:build !windows

package audit

import (
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

func ownedByCurrentUser(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Geteuid()
}

func checkDirMode(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("stat audit directory: %w", err)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("audit directory must not be writable by group or others: %s", dir)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("audit directory must be owned by the current user: %s", dir)
	}
	return nil
}

// syncDir persists the directory entries created or renamed in dir.
func syncDir(dir string) error {
	d, err := os.Open(filepath.Clean(dir))
	if err != nil {
		return err
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	return err
}
