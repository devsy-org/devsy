//go:build !windows

package dependencies

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// A final link is never followed, a pipe never blocks the open, and a device
// never becomes the controlling terminal. The file is opened for a metadata
// read only. Its content is never read.
const lookupOpenFlags = os.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK | syscall.O_NOCTTY

var errLookupFileChanged = errors.New("file changed during inspection")

type lookupOpener func(path string) (*os.File, error)

func openLookupFile(path string) (*os.File, error) {
	return os.OpenFile(filepath.Clean(path), lookupOpenFlags, 0)
}

// inspectLookupFile reports what the path names without running it. A regular
// file is identified from the descriptor that was opened, never from a second
// path lookup. Opening needs read permission, so a file that is executable but
// unreadable is reported as inaccessible.
func inspectLookupFile(ctx context.Context, path string) (lookupFile, error) {
	if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return lookupFile{}, &os.PathError{Op: "lstat", Path: path, Err: syscall.EINVAL}
	}
	first, err := lstatLookup(ctx, path)
	if err != nil {
		return lookupFile{}, err
	}
	observation := lookupFile{symlink: first.Mode()&os.ModeSymlink != 0}
	resolved, err := resolveLookupPath(ctx, path)
	if err != nil {
		return observation, err
	}
	observation.resolved = resolved
	observation.symlink = observation.symlink || resolved != filepath.Clean(path)
	observation.info, err = openedFileInfo(ctx, resolved, openLookupFile)
	if canceled := ctx.Err(); canceled != nil {
		return lookupFile{}, canceled
	}
	return observation, err
}

// openedFileInfo never opens a path that is not a regular file, because opening
// a device can have side effects. For a regular file the info comes from the
// opened descriptor.
func openedFileInfo(
	ctx context.Context,
	resolved string,
	open lookupOpener,
) (os.FileInfo, error) {
	pathInfo, err := lstatLookup(ctx, resolved)
	if err != nil || !pathInfo.Mode().IsRegular() {
		return pathInfo, err
	}
	return statOpenedFile(ctx, resolved, pathInfo, open)
}

// statOpenedFile fails when the opened file is not the one the path named a
// moment ago, so a swap between the two calls is reported instead of used. A
// freed inode number can be reused by the swapped-in file, so the opened file
// must also still be regular.
func statOpenedFile(
	ctx context.Context,
	path string,
	named os.FileInfo,
	open lookupOpener,
) (os.FileInfo, error) {
	file, err := open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if canceled := ctx.Err(); canceled != nil {
		return nil, canceled
	}
	if err != nil {
		return nil, err
	}
	if !os.SameFile(named, info) || !info.Mode().IsRegular() {
		return nil, &os.PathError{Op: "open", Path: path, Err: errLookupFileChanged}
	}
	return info, nil
}

// Resolve absolute Unix paths component by component so cancellation is checked
// between metadata calls, including link reads. In-flight syscalls cannot be interrupted.
func resolveLookupPath(ctx context.Context, path string) (string, error) {
	walk := unixLookupPath{resolved: "/", pending: path}
	for walk.pending != "" {
		part, more := walk.nextComponent()
		if part == "" {
			continue
		}
		next := filepath.Join(walk.resolved, part)
		info, target, err := lookupPathComponent(ctx, next)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if err := walk.followLink(next, target, more); err != nil {
				return "", err
			}
			continue
		}
		if more && !info.IsDir() {
			return "", &os.PathError{Op: "lstat", Path: next, Err: syscall.ENOTDIR}
		}
		walk.resolved = next
	}
	return walk.resolved, ctx.Err()
}

type unixLookupPath struct {
	resolved string
	pending  string
	links    int
}

func (w *unixLookupPath) nextComponent() (string, bool) {
	part, rest, more := strings.Cut(w.pending, "/")
	w.pending = rest
	switch part {
	case "", ".":
		return "", more
	case "..":
		w.resolved = filepath.Dir(w.resolved)
		return "", more
	default:
		return part, more
	}
}

func (w *unixLookupPath) followLink(path, target string, more bool) error {
	w.links++
	if w.links > maxLookupLinks {
		return &os.PathError{Op: "readlink", Path: path, Err: syscall.ELOOP}
	}
	if target == "" {
		return &os.PathError{Op: "readlink", Path: path, Err: syscall.ENOENT}
	}
	if more {
		target += "/" + w.pending
	}
	w.pending = target
	if filepath.IsAbs(target) {
		w.resolved = "/"
	}
	return nil
}

// maxLookupLinks is the Linux kernel limit per path. The walk follows links
// itself, so the cap also bounds the work a hostile directory can cause.
const maxLookupLinks = 40

func lookupPathComponent(ctx context.Context, path string) (os.FileInfo, string, error) {
	info, err := lstatLookup(ctx, path)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return info, "", err
	}
	target, err := os.Readlink(path)
	if canceled := ctx.Err(); canceled != nil {
		return nil, "", canceled
	}
	return info, target, err
}

func lstatLookup(ctx context.Context, path string) (os.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if canceled := ctx.Err(); canceled != nil {
		return nil, canceled
	}
	return info, err
}
