//go:build windows

package command

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const jobObjectTerminateAccess = 0x0008

var procOpenJobObjectW = windows.NewLazySystemDLL("kernel32.dll").NewProc("OpenJobObjectW")

// jobNameFor names the Job Object after the worker (e.g. devsy-up-<taskID>)
// rather than its PID: a reused PID must never inherit a predecessor's job.
func jobNameFor(name string) string {
	return name
}

func prepareBackgroundTree(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
}

// resumeBackgroundTree resumes the primary thread of a process created with
// CREATE_SUSPENDED. That process cannot spawn descendants until it has been
// assigned to its Job Object.
func resumeBackgroundTree(pid int) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return fmt.Errorf("snapshot process threads: %w", err)
	}
	defer func() { _ = windows.CloseHandle(snapshot) }()

	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	if err := windows.Thread32First(snapshot, &entry); err != nil {
		return fmt.Errorf("find primary thread for process %d: %w", pid, err)
	}
	for {
		if entry.OwnerProcessID == uint32(pid) {
			thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
			if err != nil {
				return fmt.Errorf("open primary thread %d: %w", entry.ThreadID, err)
			}
			defer func() { _ = windows.CloseHandle(thread) }()

			previous, err := windows.ResumeThread(thread)
			if err != nil {
				return fmt.Errorf("resume primary thread %d: %w", entry.ThreadID, err)
			}
			if previous != 1 {
				return fmt.Errorf("primary thread %d suspend count was %d, want 1", entry.ThreadID, previous)
			}
			return nil
		}
		if err := windows.Thread32Next(snapshot, &entry); err != nil {
			return fmt.Errorf("find primary thread for process %d: %w", pid, err)
		}
	}
}

// ownProcessTree assigns the process to a Job Object named for the worker.
// Job membership outlives intermediate parents, so tree teardown stays
// complete where taskkill /T loses track. Assignment must succeed before the
// suspended worker is resumed so descendants cannot escape the job.
func ownProcessTree(pid int, workerName string) error {
	name, err := windows.UTF16PtrFromString(jobNameFor(workerName))
	if err != nil {
		return err
	}
	job, err := windows.CreateJobObject(nil, name)
	if err != nil {
		return fmt.Errorf("create job object: %w", err)
	}
	defer func() { _ = windows.CloseHandle(job) }()

	handle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(pid),
	)
	if err != nil {
		return fmt.Errorf("open process %d: %w", pid, err)
	}
	defer func() { _ = windows.CloseHandle(handle) }()

	if err := windows.AssignProcessToJobObject(job, handle); err != nil {
		return fmt.Errorf("assign process %d to job: %w", pid, err)
	}
	return nil
}

// terminateJob kills the tree owned by the worker's Job Object. The bool
// reports whether a job existed; without one the caller falls back to
// taskkill, e.g. for workers launched before job ownership existed.
func terminateJob(workerName string) (bool, error) {
	name, err := windows.UTF16PtrFromString(jobNameFor(workerName))
	if err != nil {
		return false, err
	}
	handle, _, callErr := procOpenJobObjectW.Call(
		uintptr(jobObjectTerminateAccess),
		0,
		uintptr(unsafe.Pointer(name)),
	)
	if handle == 0 {
		if errors.Is(callErr, windows.ERROR_FILE_NOT_FOUND) {
			return false, nil
		}
		return false, fmt.Errorf("open job %s: %w", workerName, callErr)
	}
	job := windows.Handle(handle)
	defer func() { _ = windows.CloseHandle(job) }()

	if err := windows.TerminateJobObject(job, 1); err != nil {
		return false, fmt.Errorf("terminate job %s: %w", workerName, err)
	}
	return true, nil
}
