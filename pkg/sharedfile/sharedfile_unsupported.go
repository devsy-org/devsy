//go:build windows

package sharedfile

import (
	"fmt"
	"os"
	"runtime"
)

// openNoFollow has no symlink-safe open on Windows. Every sharedfile caller
// only ever runs inside the Linux container (setup-gpg, the SSH server's
// activity file, the devcontainer result file), never on a Windows host, so
// this exists solely to keep the devsy CLI binary itself cross-compiling.
func openNoFollow(string) (*os.File, error) {
	return nil, fmt.Errorf("sharedfile: not supported on %s", runtime.GOOS)
}
