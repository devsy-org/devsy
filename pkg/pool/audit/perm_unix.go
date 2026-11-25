//go:build !windows

package audit

import (
	"fmt"
	"os"
)

func checkDirMode(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("stat audit directory: %w", err)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("audit directory must not be writable by group or others: %s", dir)
	}
	return nil
}
