//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package setup

import (
	"errors"
	"fmt"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// secureContainerDataDir creates or opens the final directory component without
// following a symlink, then applies the mode through the open descriptor.
func secureContainerDataDir(dir string) error {
	parentFD, name, err := openContainerDataParent(dir)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(parentFD) }()

	if err := ensureContainerDataDir(parentFD, name); err != nil {
		return err
	}
	fd, err := openContainerDataDir(parentFD, name)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	return secureOpenedContainerDataDir(fd)
}

func openContainerDataParent(dir string) (int, string, error) {
	parent := filepath.Dir(dir)
	name := filepath.Base(dir)
	// The parent is a fixed system directory (/var or /tmp). macOS exposes
	// /tmp as a symlink, so only the final data-directory component is opened
	// with O_NOFOLLOW below.
	fd, err := unix.Open(
		parent,
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC,
		0,
	)
	if err != nil {
		return 0, "", fmt.Errorf("open parent: %w", err)
	}
	return fd, name, nil
}

func ensureContainerDataDir(parentFD int, name string) error {
	if err := unix.Mkdirat(parentFD, name, 0o755); err != nil && !errors.Is(err, unix.EEXIST) {
		return fmt.Errorf("create directory: %w", err)
	}
	return nil
}

func openContainerDataDir(parentFD int, name string) (int, error) {
	fd, err := unix.Openat(
		parentFD,
		name,
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0,
	)
	if err != nil {
		return 0, fmt.Errorf("open directory: %w", err)
	}
	return fd, nil
}

func secureOpenedContainerDataDir(fd int) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return fmt.Errorf("stat directory: %w", err)
	}
	currentUID := currentUnixUID()
	if currentUID != stat.Uid {
		return fmt.Errorf("directory is owned by uid %d", stat.Uid)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return fmt.Errorf("path is not a directory")
	}
	if err := unix.Fchmod(fd, 0o755); err != nil {
		return fmt.Errorf("chmod directory: %w", err)
	}
	return nil
}

func currentUnixUID() uint32 {
	return uint32(unix.Geteuid()) //nolint:gosec // euid is nonnegative and Unix UIDs are uint32
}
