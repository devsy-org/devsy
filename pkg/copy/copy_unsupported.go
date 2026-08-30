//go:build windows

package copy

import (
	"errors"
	"os"
	"syscall"
)

func IsUID(info os.FileInfo, uid uint32) bool {
	return true
}

// DeniedByFilesystem reports whether the platform refused the reassignment.
// IsUID short-circuits ChownR on Windows so this is rarely consulted, but a
// direct os.Lchown fails with EWINDOWS: that is "unsupported here", a
// tolerated denial, not a hard failure.
func DeniedByFilesystem(err error) bool {
	return errors.Is(err, syscall.EWINDOWS)
}

// Unsupported reports whether chown is not a meaningful operation on this
// platform at all (EWINDOWS), as opposed to a real filesystem denial.
func Unsupported(err error) bool {
	return errors.Is(err, syscall.EWINDOWS)
}

func Lchown(info os.FileInfo, sourcePath, destPath string) error {
	return nil
}
