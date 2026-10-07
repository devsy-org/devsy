//go:build windows

package main

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

type processInfo struct {
	PID  uint32
	Name string
}

func matchingWindowsProcesses() []processInfo {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer func() { _ = windows.CloseHandle(snapshot) }()
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return nil
	}
	var found []processInfo
	for {
		name := windows.UTF16ToString(entry.ExeFile[:])
		if isInterestingWindowsProcess(name) {
			found = append(found, processInfo{PID: entry.ProcessID, Name: name})
		}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			break
		}
	}
	return found
}

func isInterestingWindowsProcess(name string) bool {
	base := strings.TrimSuffix(strings.ToLower(name), ".exe")
	switch base {
	case "podman", "wsl", "gvproxy", "win-sshproxy":
		return true
	default:
		return false
	}
}
