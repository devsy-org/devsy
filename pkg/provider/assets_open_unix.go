//go:build unix

package provider

import (
	"os"
	"syscall"
)

// openLocalIconFile cannot block if the target becomes a FIFO after the
// pre-open Stat. The caller still checks the opened file's type and size.
func openLocalIconFile(root *os.Root, target string) (*os.File, error) {
	return root.OpenFile(target, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
