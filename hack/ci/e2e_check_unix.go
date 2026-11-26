//go:build !windows

package main

import (
	"fmt"
	"os"
	"syscall"
)

// requireOwnedBy fails unless path is owned by uid.
func requireOwnedBy(path string, uid int) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", errFocusedOwner, path, err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%w: %s has no owner information", errFocusedOwner, path)
	}
	if int(stat.Uid) != uid {
		return fmt.Errorf(
			"%w: %s is owned by uid %d, want %d",
			errFocusedOwner,
			path,
			stat.Uid,
			uid,
		)
	}
	return nil
}
