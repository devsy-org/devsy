package ghcredentials

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

type ownerLock struct{ file *os.File }

// Kernel ownership follows the inherited descriptor, independently of the
// cache filesystem's lock semantics. The real gh keeps this session alive
// even if its wrapper is killed; no socket accepts or transmits credentials.
func tryOwner(path string) (*ownerLock, bool, error) {
	key := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", os.Getuid(), path)))
	fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, false, err
	}
	err = unix.Bind(fd, &unix.SockaddrUnix{Name: fmt.Sprintf("@devsy-gh-%x", key[:20])})
	if err == nil {
		return &ownerLock{file: os.NewFile(uintptr(fd), "gh-session-owner")}, true, nil
	}
	_ = unix.Close(fd)
	if errors.Is(err, unix.EADDRINUSE) {
		return nil, false, nil
	}
	return nil, false, err
}

func (l *ownerLock) Close() error { return l.file.Close() }
