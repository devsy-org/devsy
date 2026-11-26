//go:build !linux && !windows

package ghcredentials

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

type ownerLock struct{ file *os.File }

func tryOwner(path string) (*ownerLock, bool, error) {
	f, err := ownerFile(path)
	if err != nil {
		return nil, false, err
	}
	err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == nil {
		return &ownerLock{file: f}, true, nil
	}
	_ = f.Close()
	if err == unix.EWOULDBLOCK {
		return nil, false, nil
	}
	return nil, false, err
}

func (l *ownerLock) Close() error { return l.file.Close() }

func ownerFile(path string) (*os.File, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return root.OpenFile(filepath.Base(path), os.O_CREATE|os.O_RDWR, 0o600)
}
