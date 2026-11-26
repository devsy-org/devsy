package provider

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
)

// StatUpdatePath retains Root confinement while avoiding a Windows Root.Stat
// panic when a terminal relative link normalizes to its own parent directory.
// Windows cleans link targets before traversal; POSIX must retain kernel ordering.
func StatUpdatePath(root *os.Root, name string) (fs.FileInfo, error) {
	if runtime.GOOS != "windows" {
		return root.Stat(name)
	}
	original := name
	// Match Root's link traversal bound without following arbitrary targets.
	for remaining := 8; ; remaining-- {
		parent, alias := updatePathParentAlias(root, name)
		if !alias {
			return root.Stat(name)
		}
		if remaining == 0 {
			return nil, &os.PathError{Op: "stat", Path: original, Err: syscall.ELOOP}
		}
		name = parent
	}
}

func updatePathParentAlias(root *os.Root, name string) (string, bool) {
	target, err := root.Readlink(name)
	if err != nil || filepath.IsAbs(target) || filepath.VolumeName(target) != "" {
		return "", false
	}
	parent := filepath.Dir(name)
	return parent, parent != name && filepath.Join(parent, target) == parent
}
