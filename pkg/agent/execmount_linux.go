//go:build linux

package agent

import "golang.org/x/sys/unix"

func dirAllowsExec(dir string) (bool, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(dir, &stat); err != nil {
		return false, err
	}
	return stat.Flags&unix.ST_NOEXEC == 0, nil
}
