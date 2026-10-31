//go:build !windows

package workspacelogs

import (
	"io/fs"
	"os"
	"syscall"
)

func ownedByCurrentUser(info fs.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)

	return ok && int(st.Uid) == os.Getuid()
}
