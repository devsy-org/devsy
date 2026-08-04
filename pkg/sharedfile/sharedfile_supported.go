//go:build linux || darwin || unix

package sharedfile

import (
	"os"
	"syscall"
)

// openNoFollow opens path without following a trailing symlink, so the
// caller can chmod/read/write the resulting descriptor's inode regardless
// of what path later resolves to. createMode is used only when flag
// includes os.O_CREATE. This always adds O_NOFOLLOW and O_NONBLOCK — the
// latter so a FIFO planted at path fails the open immediately
// (ENXIO/EAGAIN) instead of blocking forever waiting for a peer. The
// caller must still reject non-regular files after Stat: O_NONBLOCK only
// keeps the open from hanging, it does not stop a FIFO fd from being
// returned.
func openNoFollow(path string, flag int, createMode os.FileMode) (*os.File, error) {
	//nolint:gosec // callers intentionally open a fixed coordination-file path
	return os.OpenFile(path, flag|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, createMode)
}
