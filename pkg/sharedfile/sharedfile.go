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
	"os"
)

// EnsureMode ensures path exists with exactly mode permissions, creating it
// if absent. Skips the chmod when the file's mode already matches: chmod
// requires ownership (or root) even when the requested mode wouldn't
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
// the chmod entirely when it's already correct. Opens path without
// following a symlink and chmods the resulting descriptor rather than the
// path, so a symlink swapped in after a check-then-chmod by path couldn't
// redirect the chmod onto an arbitrary target — path is a fixed,
// world-writable coordination file any container user could otherwise race.
// Also rejects a FIFO or other non-regular file at path: opening a FIFO
// with no writer would otherwise block this call forever.
func WidenIfNeeded(path string, mode os.FileMode) error {
	f, err := openNoFollow(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("refusing to chmod %s: not a regular file (mode %s)", path, info.Mode())
	}
	if info.Mode().Perm() == mode.Perm() {
		return nil
	}
	if err := f.Chmod(mode); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}

// createIfMissing creates path at mode if it doesn't already exist. Leaves
// an existing file untouched — its mode is WidenIfNeeded's job — so this
// never races a concurrent creator into truncating their write.
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
