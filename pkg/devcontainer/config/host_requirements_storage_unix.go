//go:build !windows

package config

import "syscall"

func availableStorageBytes(path string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), nil //nolint:gosec // Bsize type varies by platform
}
