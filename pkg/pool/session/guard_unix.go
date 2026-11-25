//go:build !windows

package session

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// nonBlock keeps an open from hanging on a FIFO planted at a path; the
// regular-file check after the open then refuses it.
const nonBlock = syscall.O_NONBLOCK

func checkPlatform() error { return nil }

// checkRoot refuses a directory that other users can reach or that another
// user owns. It checks the open directory, not a path that could change.
func checkRoot(root *os.Root) error {
	info, err := root.Stat(".")
	if err != nil {
		return fmt.Errorf("stat pool directory: %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("pool directory must be accessible only by its owner: %s", root.Name())
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("pool directory must be owned by the current user: %s", root.Name())
	}
	return nil
}

// syncRoot persists the entries created or renamed in the directory. A
// filesystem that cannot fsync a directory has no stronger guarantee to wait
// for.
func syncRoot(root *os.Root) error {
	d, err := root.Open(".")
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
