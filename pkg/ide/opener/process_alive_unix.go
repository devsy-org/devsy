//go:build !windows

package opener

import (
	"os"
	"syscall"
)

// isProcessAlive reports whether a process with the given PID is alive AND
// owned by the current user. On Unix, FindProcess always succeeds, so
// liveness is confirmed by sending signal 0. A foreign-UID PID (EPERM) is
// treated as not-alive so respawn proceeds cleanly — at worst this orphans a
// foreign-UID tunnel, which is preferable to silently blocking re-launch.
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}
