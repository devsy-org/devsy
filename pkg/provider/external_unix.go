//go:build !windows

package provider

import "golang.org/x/sys/unix"

func checkExternalExecuteAccess(path string) error {
	return unix.Faccessat(unix.AT_FDCWD, path, unix.X_OK, unix.AT_EACCESS)
}
