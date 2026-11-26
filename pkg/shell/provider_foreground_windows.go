package shell

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

var (
	providerKernel32              = windows.NewLazySystemDLL("kernel32.dll")
	providerSetConsoleCtrlHandler = providerKernel32.NewProc("SetConsoleCtrlHandler")
	providerGetConsoleCP          = providerKernel32.NewProc("GetConsoleCP")
	providerConsoleHandler        = syscall.NewCallback(func(event uint32) uintptr {
		if event == 0 || event == 1 {
			return 1
		}
		return 0
	})
)

func PrepareProviderShellForeground(parentPID int) (func(), error) {
	if parentPID <= 0 || uint64(parentPID) > uint64(^uint32(0)) || os.Getppid() != parentPID {
		return nil, fmt.Errorf("provider shell parent mismatch")
	}
	cp, _, _ := providerGetConsoleCP.Call()
	if cp == 0 {
		return func() {}, nil
	}
	ok, _, err := providerSetConsoleCtrlHandler.Call(providerConsoleHandler, 1)
	if ok == 0 {
		return nil, fmt.Errorf("register provider console handler: %w", err)
	}
	ok, _, err = providerSetConsoleCtrlHandler.Call(0, 0)
	if ok == 0 {
		_, _, _ = providerSetConsoleCtrlHandler.Call(providerConsoleHandler, 0)
		return nil, fmt.Errorf("enable child console interrupts: %w", err)
	}
	return func() { _, _, _ = providerSetConsoleCtrlHandler.Call(providerConsoleHandler, 0) }, nil
}

func newProviderShellParentMonitor(parentPID int) (func() bool, func(), error) {
	if parentPID <= 0 || uint64(parentPID) > uint64(^uint32(0)) {
		return nil, nil, fmt.Errorf("invalid parent PID %d", parentPID)
	}
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(parentPID))
	if err != nil {
		return nil, nil, fmt.Errorf("open parent process: %w", err)
	}
	return func() bool {
		result, err := windows.WaitForSingleObject(handle, 0)
		return err == nil && result == uint32(windows.WAIT_TIMEOUT)
	}, func() { _ = windows.CloseHandle(handle) }, nil
}
