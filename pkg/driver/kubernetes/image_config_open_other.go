//go:build !unix && !windows

package kubernetes

import "os"

func openImageConfig(path string) (*os.File, error) {
	return os.Open(path)
}
