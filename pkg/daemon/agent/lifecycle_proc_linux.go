//go:build linux

package agent

import (
	"os"
	"strconv"
	"strings"
)

// procStartTicks returns field 22 of /proc/<pid>/stat, which distinguishes a
// process from a later one that reuses its PID.
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
	return fields[19]
}
