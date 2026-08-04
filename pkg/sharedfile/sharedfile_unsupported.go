//go:build windows

package sharedfile

import (
	"fmt"
	"os"
	"runtime"
)

// openNoFollow has no symlink-safe open on Windows.
func openNoFollow(string) (*os.File, error) {
	return nil, fmt.Errorf("sharedfile: not supported on %s", runtime.GOOS)
}
