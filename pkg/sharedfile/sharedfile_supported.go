//go:build linux || darwin || unix

package sharedfile

import (
	"os"
	"syscall"
)

// openNoFollow opens path without following a trailing symlink, so the
// caller can chmod the resulting descriptor's inode regardless of what
// path later resolves to. Opens O_RDONLY, not O_WRONLY: fchmod only cares
// about ownership, not how the fd was opened, and a coordination-file mode
// (e.g. 0644, 0666) always grants read to the "already correct, skip the
// chmod" caller even when it doesn't grant that caller write.
func openNoFollow(path string) (*os.File, error) {
	//nolint:gosec // callers intentionally widen a fixed coordination-file path
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
}
