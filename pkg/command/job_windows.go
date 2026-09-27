//go:build windows

package command

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const jobObjectTerminateAccess = 0x0008

const jobObjectQueryAccess = 0x0004

const jobTerminationTimeout = 5 * time.Second

type jobAccountingInfo struct {
	totalUserTime             int64
	totalKernelTime           int64
	thisPeriodTotalUserTime   int64
	thisPeriodTotalKernelTime int64
	totalPageFaultCount       uint32
	totalProcesses            uint32
	activeProcesses           uint32
	totalTerminatedProcesses  uint32
}

var (
	procOpenJobObjectW = windows.NewLazySystemDLL("kernel32.dll").
				NewProc("OpenJobObjectW")
	procIsProcessInJobForTermination = windows.NewLazySystemDLL("kernel32.dll").
						NewProc("IsProcessInJob")
)

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
				return fmt.Errorf(
					"primary thread %d suspend count was %d, want 1",
					entry.ThreadID,
					previous,
				)
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

	// The worker keeps the name open for cancellation; its exit also closes
	// the last handle, which terminates any descendants it leaves behind.
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)),
		uint32(unsafe.Sizeof(limits)),
	); err != nil {
		return fmt.Errorf("configure worker job %s: %w", workerName, err)
	}

	handle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_DUP_HANDLE,
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
	var workerJob windows.Handle
	if err := windows.DuplicateHandle(
		windows.CurrentProcess(),
		job,
		handle,
		&workerJob,
		0,
		false,
		windows.DUPLICATE_SAME_ACCESS,
	); err != nil {
		return fmt.Errorf("keep worker job open in process %d: %w", pid, err)
	}
	return nil
}

// terminateJob kills the tree owned by the worker's Job Object. The bool
// reports whether a job existed; without one the caller falls back to
// taskkill, e.g. for workers launched before job ownership existed.
func terminateJob(workerName string) (bool, error) {
	job, found, err := openWorkerJob(workerName)
	if err != nil || !found {
		return found, err
	}
	defer func() { _ = windows.CloseHandle(job) }()
	return terminateJobHandle(job, workerName)
}

func terminateJobForPID(workerName string, pid int) (bool, error) {
	job, found, err := openWorkerJob(workerName)
	if err != nil || !found {
		return found, err
	}
	defer func() { _ = windows.CloseHandle(job) }()

	inJob, err := processBelongsToJob(pid, job)
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return false, fmt.Errorf("worker process %d exited before job membership was verified", pid)
	}
	if err != nil {
		return false, err
	}
	if !inJob {
		return false, fmt.Errorf("process %d does not belong to worker job %s", pid, workerName)
	}
	return terminateJobHandle(job, workerName)
}

func processBelongsToJob(pid int, job windows.Handle) (bool, error) {
	process, err := windows.OpenProcess(
		windows.PROCESS_QUERY_LIMITED_INFORMATION,
		false,
		uint32(pid),
	)
	if err != nil {
		return false, fmt.Errorf("open worker process %d: %w", pid, err)
	}
	defer func() { _ = windows.CloseHandle(process) }()
	var inJob int32
	result, _, callErr := procIsProcessInJobForTermination.Call(
		uintptr(process),
		uintptr(job),
		uintptr(unsafe.Pointer(&inJob)),
	)
	if result == 0 {
		return false, fmt.Errorf("verify process %d in job: %w", pid, callErr)
	}
	return inJob != 0, nil
}

func openWorkerJob(workerName string) (windows.Handle, bool, error) {
	name, err := windows.UTF16PtrFromString(jobNameFor(workerName))
	if err != nil {
		return 0, false, err
	}
	handle, _, callErr := procOpenJobObjectW.Call(
		uintptr(jobObjectTerminateAccess|jobObjectQueryAccess),
		0,
		uintptr(unsafe.Pointer(name)),
	)
	if handle == 0 {
		if errors.Is(callErr, windows.ERROR_FILE_NOT_FOUND) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("open job %s: %w", workerName, callErr)
	}
	return windows.Handle(handle), true, nil
}

func terminateJobHandle(job windows.Handle, workerName string) (bool, error) {
	if err := windows.TerminateJobObject(job, 1); err != nil {
		return false, fmt.Errorf("terminate job %s: %w", workerName, err)
	}
	deadline := time.Now().Add(jobTerminationTimeout)
	for {
		var info jobAccountingInfo
		if err := windows.QueryInformationJobObject(
			job,
			windows.JobObjectBasicAccountingInformation,
			uintptr(unsafe.Pointer(&info)),
			uint32(unsafe.Sizeof(info)),
			nil,
		); err != nil {
			return false, fmt.Errorf("query job %s processes: %w", workerName, err)
		}
		if info.activeProcesses == 0 {
			return true, nil
		}
		if time.Now().After(deadline) {
			return false, fmt.Errorf(
				"job %s still has %d active processes after termination",
				workerName,
				info.activeProcesses,
			)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
