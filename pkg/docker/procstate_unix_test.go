//go:build !windows

package docker

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"syscall"
)

// processRunning reports whether pid is a live, non-zombie process. A killed
// orphan awaiting reaping by an init that never reaps still counts as gone.
func processRunning(pid int) bool {
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return false
	}
	if stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil {
		if i := bytes.LastIndexByte(stat, ')'); i >= 0 && i+2 < len(stat) {
			return stat[i+2] != 'Z'
		}
	}
	return true
}
