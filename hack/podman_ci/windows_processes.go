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

// terminateStrayProcessesHook is the seam setupWindows and armWatchdog call
// through; tests override it to avoid sweeping real podman/wsl/gvproxy/
// win-sshproxy processes that may be running on a developer's machine for
// unrelated work.
var terminateStrayProcessesHook = terminateStrayWindowsProcesses

// terminateStrayWindowsProcesses forcefully terminates any Podman/WSL helper
// processes still running system-wide (podman, wsl, gvproxy, win-sshproxy).
// It is called when devsy-ci-podman.exe is about to exit after a terminal
// failure (including the hard watchdog deadline) so that no descendant
// process is left behind holding handles or keeping a VM alive past our own
// bootstrap budget: per-command timeouts deliberately leave descendants
// running to let recovery/readiness polling observe a still-booting
// machine, but once setup gives up for good there is no further attempt to
// reap them.
func terminateStrayWindowsProcesses(log *Logger) {
	terminateWindowsProcesses(matchingWindowsProcesses(), log)
}

// terminateWindowsProcesses force-kills exactly the given processes. It is
// split out from terminateStrayWindowsProcesses so tests can exercise the
// real OpenProcess/TerminateProcess kill mechanics against a single
// known-owned helper process without sweeping the whole host.
func terminateWindowsProcesses(processes []processInfo, log *Logger) {
	for _, process := range processes {
		handle, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, process.PID)
		if err != nil {
			log.Printf(
				"[podman-windows] stray_process_open_failed pid=%d name=%s err=%v",
				process.PID,
				process.Name,
				err,
			)
			continue
		}
		killErr := windows.TerminateProcess(handle, 124)
		_ = windows.CloseHandle(handle)
		log.Printf(
			"[podman-windows] stray_process_terminated pid=%d name=%s err=%v",
			process.PID,
			process.Name,
			killErr,
		)
	}
}
