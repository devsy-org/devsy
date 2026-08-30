//go:build linux || darwin || unix

package copy

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func IsUID(info os.FileInfo, uid uint32) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uid
}

// DeniedByFilesystem reports whether err means the filesystem refused the
// reassignment (insufficient privilege or a read-only share).
func DeniedByFilesystem(err error) bool {
	return errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EROFS)
}

// Unsupported reports whether err means chown itself isn't a meaningful
// operation on this platform (Windows only). On unix, chown is always a
// real operation, so any failure here is a genuine denial, never this.
func Unsupported(error) bool {
	return false
}

func Lchown(info os.FileInfo, sourcePath, destPath string) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("failed to get raw syscall.Stat_t data for %q", sourcePath)
	}
	if err := os.Lchown(destPath, int(stat.Uid), int(stat.Gid)); err != nil {
		return err
	}
	return nil
}
