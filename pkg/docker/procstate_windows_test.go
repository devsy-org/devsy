//go:build windows

package docker

import (
	"errors"

	"golang.org/x/sys/windows"
)

const stillActive = 259 // STILL_ACTIVE exit code

// processRunning reports whether pid is a live process.
func processRunning(pid int) bool {
	h, err := windows.OpenProcess(
		windows.PROCESS_QUERY_LIMITED_INFORMATION,
		false,
		uint32(pid), //nolint:gosec // pid fits uint32
	)
	if err != nil {
		// No such process once it has exited and every handle is closed.
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return true
	}
	return code == stillActive
}
