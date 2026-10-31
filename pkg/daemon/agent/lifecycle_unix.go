//go:build !windows

package agent

import (
	"os"
	"syscall"
)

func fileOwner(fi os.FileInfo) (int, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(st.Uid), true
}

// openNoFollow refuses a trailing symlink and never blocks on a FIFO.
func openNoFollow(path string, flag int, mode os.FileMode) (*os.File, error) {
	//nolint:gosec // callers pass fixed lifecycle record paths
	return os.OpenFile(path, flag|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, mode)
}

func processExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
