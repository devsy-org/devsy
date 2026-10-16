//go:build windows

package docker

import (
	"errors"

	"golang.org/x/sys/windows"
)

// processRunning reports whether pid is a live process.
func processRunning(pid int) bool {
	h, err := windows.OpenProcess(
		windows.SYNCHRONIZE,
		false,
		uint32(pid), //nolint:gosec // pid fits uint32
	)
	if err != nil {
		// No such process once it has exited and every handle is closed.
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	event, err := windows.WaitForSingleObject(h, 0)
	if err != nil {
		return true
	}
	return event != windows.WAIT_OBJECT_0
}
