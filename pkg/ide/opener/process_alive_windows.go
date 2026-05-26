//go:build windows

package opener

import "golang.org/x/sys/windows"

// stillActive is the exit code returned by GetExitCodeProcess for a running process.
const stillActive uint32 = 259

// isProcessAlive reports whether a process with the given PID is still running.
// os.FindProcess is not sufficient on Windows: OpenProcess succeeds for any
// visible PID, including processes that have inherited a reused PID. Query the
// exit code and compare against STILL_ACTIVE to distinguish.
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}
