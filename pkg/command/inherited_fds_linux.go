package command

import (
	"math"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

const firstInheritedFD = 3

var closeRange = unix.CloseRange

func MarkInheritedFDsCloseOnExec() {
	err := closeRange(firstInheritedFD, math.MaxUint32, unix.CLOSE_RANGE_CLOEXEC)
	if err != nil {
		markProcFDsCloseOnExec()
	}
}

func markProcFDsCloseOnExec() {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return
	}
	for _, entry := range entries {
		fd, err := strconv.Atoi(entry.Name())
		if err != nil || fd < firstInheritedFD {
			continue
		}
		unix.CloseOnExec(fd)
	}
}
