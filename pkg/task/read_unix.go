//go:build !windows

package task

import "os"

func openTaskFile(path string) (*os.File, error) {
	return os.Open(path) // #nosec G304 G703 -- path validated by Store.path
}
