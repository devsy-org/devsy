//go:build windows

package agent

import "os"

func fileOwner(os.FileInfo) (int, bool) { return 0, false }

func openNoFollow(path string, flag int, mode os.FileMode) (*os.File, error) {
	//nolint:gosec // callers pass fixed lifecycle record paths
	return os.OpenFile(path, flag, mode)
}

func processExists(int) bool { return false }
