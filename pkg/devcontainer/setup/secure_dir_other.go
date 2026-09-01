//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package setup

import (
	"fmt"
	"os"
)

// secureContainerDataDir provides the closest available protection on targets
// without descriptor-based no-follow directory operations.
func secureContainerDataDir(dir string) error {
	info, err := os.Lstat(dir)
	switch {
	case err == nil:
		if info.IsDir() {
			return os.Chmod(dir, 0o755)
		}
		return fmt.Errorf("path is not a directory")
	case !os.IsNotExist(err):
		return err
	}

	if err := os.Mkdir(dir, 0o755); err != nil {
		return err
	}
	return nil
}
