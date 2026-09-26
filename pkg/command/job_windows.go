//go:build windows

package command

import (
	"errors"
	"fmt"
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

// ownProcessTree assigns the process to a Job Object named for the worker.
// Job membership outlives intermediate parents, so tree teardown stays
// complete where taskkill /T loses track. Assignment failure is non-fatal;
// termination then falls back to taskkill /T /F.
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
