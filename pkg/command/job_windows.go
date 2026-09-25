//go:build windows

package command

import (
	"errors"
	"fmt"
	"strconv"
	"unsafe"

	"golang.org/x/sys/windows"
)

const jobObjectTerminateAccess = 0x0008

var procOpenJobObjectW = windows.NewLazySystemDLL("kernel32.dll").NewProc("OpenJobObjectW")

// jobNameForPID names the Job Object owning the process tree rooted at pid,
// so cancellation can recover the job from the persisted PID alone.
func jobNameForPID(pid int) string {
	return "devsy-worker-" + strconv.Itoa(pid)
}

// ownProcessTree assigns the process to a Job Object named after its PID.
// Parent-based termination (taskkill /T) loses track of descendants whose
// intermediate parent already exited; job membership survives that, so tree
// teardown stays complete. The launcher closes its handle right away: the
// job lives as long as it has member processes, which is what keeps the
// detached worker alive after the launcher exits. Assignment failure is
// non-fatal; termination then falls back to taskkill /T /F.
func ownProcessTree(pid int) error {
	name, err := windows.UTF16PtrFromString(jobNameForPID(pid))
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

// terminateJob kills the process tree owned by the Job Object named for pid.
// The bool reports whether a job existed; without one the caller falls back
// to taskkill, e.g. for workers launched before job ownership existed.
func terminateJob(pid int) (bool, error) {
	name, err := windows.UTF16PtrFromString(jobNameForPID(pid))
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
		return false, fmt.Errorf("open job for pid %d: %w", pid, callErr)
	}
	job := windows.Handle(handle)
	defer func() { _ = windows.CloseHandle(job) }()

	if err := windows.TerminateJobObject(job, 1); err != nil {
		return false, fmt.Errorf("terminate job for pid %d: %w", pid, err)
	}
	return true, nil
}
