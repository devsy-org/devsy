//go:build linux || darwin || unix

package sharedfile

import (
	"os"
	"syscall"
)

// openNoFollow opens path for writing without following a trailing
// symlink, so the caller can chmod the resulting descriptor's inode
// regardless of what path later resolves to.
func openNoFollow(path string) (*os.File, error) {
	//nolint:gosec // callers intentionally widen a fixed coordination-file path
	return os.OpenFile(path, os.O_WRONLY|syscall.O_NOFOLLOW, 0)
}
