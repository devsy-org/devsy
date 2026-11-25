//go:build !windows

package audit

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

const (
	noFollow = syscall.O_NOFOLLOW
	// nonBlock keeps an open from hanging on a FIFO planted at a path; the
	// regular-file check after the open then refuses it.
	nonBlock = syscall.O_NONBLOCK
)

func checkPlatform() error { return nil }

func ownedByCurrentUser(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Geteuid()
}

func checkDirMode(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("stat audit directory: %w", err)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("audit directory must not be writable by group or others: %s", dir)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("audit directory must be owned by the current user: %s", dir)
	}
	return nil
}

// fsyncDir is the directory fsync, replaceable so tests can inject the errors
// some filesystems return.
var fsyncDir = func(d *os.File) error { return d.Sync() }

// syncDir persists the directory entries created or renamed in dir. A
// filesystem that cannot fsync a directory is not an error, see
// tolerateUnsupportedSync.
func syncDir(dir string) error {
	d, err := os.Open(filepath.Clean(dir))
	if err != nil {
		return err
	}
	err = tolerateUnsupportedSync(fsyncDir(d))
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	return err
}

// tolerateUnsupportedSync drops the EINVAL and ENOTSUP that some filesystems
// and network mounts return for a directory fsync: they hold no stronger
// guarantee to wait for. Every other error still fails.
func tolerateUnsupportedSync(err error) error {
	if errors.Is(err, syscall.EINVAL) ||
		errors.Is(err, syscall.ENOTSUP) ||
		errors.Is(err, syscall.EOPNOTSUPP) {
		return nil
	}
	return err
}
