// Package sharedfile hardens the standard Unix pattern of coordinating
// between processes running as different users through a world-writable
// file in a shared, trusted location (e.g. /tmp) — the same idea behind
// /tmp/.X11-unix or /var/run/utmp. A devsy container runs commands over SSH
// sessions authenticated as either root or the workspace's remoteUser
// (pkg/ssh/server/ssh_container.go sets the process credential directly
// from the authenticated session user), so any file two of those sessions
// both touch needs its permissions to survive being created by either one.
//
// The pattern has two failure modes this package exists to close:
//   - Whichever process creates the file first can lock every other user
//     out, because file creation is subject to the process umask.
//   - A symlink planted at the file's path redirects Chmod onto an
//     arbitrary target, since Chmod follows symlinks — dangerous for a
//     fixed, predictable, world-writable path any container user can
//     pre-create.
package sharedfile

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// EnsureMode ensures path exists with exactly mode permissions, creating it
// if absent. Skips the chmod when the file's mode already matches: chmod
// requires ownership (or root) even when the requested mode would not
// change, so skipping it when unnecessary avoids EPERM for a non-owning
// acquirer of an already-correctly-moded file.
//
// Rejects a path that resolves to a symlink rather than following it.
func EnsureMode(path string, mode os.FileMode) error {
	if err := createIfMissing(path, mode); err != nil {
		return err
	}
	return WidenIfNeeded(path, mode)
}

// WidenIfNeeded chmods path to mode if its current mode differs, skipping
// the chmod entirely when it is already correct. Opens path without
// following a symlink and chmods the resulting descriptor rather than the
// path, so a symlink swapped in after a check-then-chmod by path could not
// redirect the chmod onto an arbitrary target.
func WidenIfNeeded(path string, mode os.FileMode) error {
	f, err := openNoFollowRegular(path, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if info.Mode().Perm() == mode.Perm() {
		return nil
	}
	if err := f.Chmod(mode); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}

// ReadFile reads path the same way os.ReadFile does, but refuses to follow
// a symlink or block on a FIFO planted at path.
func ReadFile(path string) ([]byte, error) {
	f, err := openNoFollowRegular(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(f)
}

// WriteFile writes data to path at mode the same way os.WriteFile does,
// creating path if absent, but refuses to follow a symlink or block on a
// FIFO already at path. O_NOFOLLOW still applies when O_CREATE is also
// set: an existing symlink is rejected rather than followed, while a
// genuinely missing path is created as a fresh regular file.
func WriteFile(path string, data []byte, mode os.FileMode) (err error) {
	f, err := openNoFollowRegular(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := f.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("close %s: %w", path, closeErr)
		}
	}()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if info.Mode().Perm() != mode.Perm() {
		if err := f.Chmod(mode); err != nil {
			return fmt.Errorf("chmod %s: %w", path, err)
		}
	}
	return nil
}

// openNoFollowRegular opens path with flag (plus the no-follow/non-blocking
// guards openNoFollow always adds) and rejects the result if it is not a
// regular file — a FIFO would otherwise pass the open (O_NONBLOCK just
// keeps that from hanging) and reach a caller expecting file content.
// createMode is only used when flag includes os.O_CREATE.
func openNoFollowRegular(path string, flag int, createMode os.FileMode) (*os.File, error) {
	f, err := openNoFollow(path, flag, createMode)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf(
			"refusing to use %s: not a regular file (mode %s)", path, info.Mode(),
		)
	}
	return f, nil
}

// createIfMissing creates path at mode if it does not already exist. Leaves
// an existing file untouched.
func createIfMissing(path string, mode os.FileMode) error {
	//nolint:gosec // callers intentionally create a fixed, world-accessible coordination file
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return fmt.Errorf("create %s: %w", path, err)
	}
	return f.Close()
}
