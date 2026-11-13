//go:build !windows

package dependencies

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func inspectLookupFile(ctx context.Context, path string) (lookupFile, error) {
	if err := ctx.Err(); err != nil {
		return lookupFile{}, err
	}
	info, err := os.Lstat(path)
	if canceled := ctx.Err(); canceled != nil {
		return lookupFile{}, canceled
	}
	if err != nil {
		return lookupFile{}, err
	}
	observation := lookupFile{symlink: info.Mode()&os.ModeSymlink != 0}
	resolved, err := resolveLookupPath(ctx, path)
	if err != nil {
		return observation, err
	}
	observation.symlink = observation.symlink || resolved != filepath.Clean(path)
	observation.resolved = resolved
	if err := ctx.Err(); err != nil {
		return lookupFile{}, err
	}
	observation.info, err = os.Stat(resolved)
	if canceled := ctx.Err(); canceled != nil {
		return lookupFile{}, canceled
	}
	return observation, err
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
	if w.links > 255 {
		return &os.PathError{Op: "readlink", Path: path, Err: syscall.ELOOP}
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

func lookupPathComponent(ctx context.Context, path string) (os.FileInfo, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	info, err := os.Lstat(path)
	if canceled := ctx.Err(); canceled != nil {
		return nil, "", canceled
	}
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return info, "", err
	}
	target, err := os.Readlink(path)
	if canceled := ctx.Err(); canceled != nil {
		return nil, "", canceled
	}
	return info, target, err
}
