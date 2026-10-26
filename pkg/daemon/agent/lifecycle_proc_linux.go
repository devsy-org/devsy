//go:build linux

package agent

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// procStartTicks returns the start identity of a live process from /proc.
func procStartTicks(pid int) string {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat") // #nosec G304
	if err != nil {
		return ""
	}
	stat := string(raw)
	// The command name may contain spaces; fields resume after the last ')'.
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return ""
	}
	fields := strings.Fields(stat[end+1:])
	// fields[0] is state (field 3), so start time (field 22) is fields[19].
	if len(fields) < 20 {
		return ""
	}
	// Zombies retain both PID and start time until their living parent reaps them.
	if fields[0] == "Z" || fields[0] == "X" || fields[0] == "x" {
		return ""
	}
	return fields[19]
}

func processStartIdentity(pid int) (string, error) {
	ticks := procStartTicks(pid)
	if ticks == "" {
		return "", fmt.Errorf(
			"%w: cannot establish live process identity from /proc/%d/stat",
			ErrLifecycleOwnerMissing,
			pid,
		)
	}
	return ticks, nil
}

func sameProcess(pid int, startTicks string) bool {
	return startTicks != "" && processExists(pid) && procStartTicks(pid) == startTicks
}
