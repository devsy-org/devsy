//go:build !windows

package channel

import (
	"errors"
	"os"
	"syscall"
)

var errParentNotPrivate = errors.New(
	"egress socket directory must be owned by the pool and closed to others",
)

// checkPrivateDir refuses a directory another user could write into or read,
// because the socket is created there before its mode is set.
func checkPrivateDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return errParentNotPrivate
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Geteuid() {
		return errParentNotPrivate
	}
	return nil
}
