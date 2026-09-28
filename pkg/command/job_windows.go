//go:build windows

package command

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/devsy-org/devsy/pkg/config"
	"golang.org/x/sys/windows"
)

const jobObjectTerminateAccess = 0x0008

const jobObjectQueryAccess = 0x0004

const jobTerminationTimeout = 5 * time.Second

const detachedTaskWorkerPrefix = "devsy-up-"

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
	procCreateJobObjectForWorker = windows.NewLazySystemDLL("kernel32.dll").
					NewProc("CreateJobObjectW")
	procOpenJobObjectW = windows.NewLazySystemDLL("kernel32.dll").
				NewProc("OpenJobObjectW")
	procIsProcessInJobForTermination = windows.NewLazySystemDLL("kernel32.dll").
						NewProc("IsProcessInJob")
)

func jobNameFor(name string) (string, error) {
	dataDir := os.Getenv(config.EnvHome)
	if dataDir == "" {
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData == "" {
			return "", errors.New(
				"resolve Devsy data directory for job object: LOCALAPPDATA is not set",
			)
		}
		dataDir = filepath.Join(localAppData, config.RepoName)
	}
	canonicalDir, err := canonicalDirectoryPath(dataDir)
	if err != nil {
		return "", fmt.Errorf("resolve Devsy data directory for job object: %w", err)
	}
	dataDirHash := sha256.Sum256([]byte(canonicalDir))
	return "devsy-" + hex.EncodeToString(dataDirHash[:8]) + "-" + name, nil
}

func canonicalDirectoryPath(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	pathPtr, err := windows.UTF16PtrFromString(absPath)
	if err != nil {
		return "", err
	}
	handle, err := windows.CreateFile(
		pathPtr,
		windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) ||
			errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			return normalizedWindowsPath(absPath), nil
		}
		return "", err
	}
	defer func() { _ = windows.CloseHandle(handle) }()

	const maxPath = 32768
	buffer := make([]uint16, maxPath)
	length, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
	if err != nil {
		return "", err
	}
	if length >= uint32(len(buffer)) {
		return "", fmt.Errorf("canonical directory path exceeds %d UTF-16 code units", len(buffer))
	}
	return windows.UTF16ToString(buffer[:length]), nil
}

func normalizedWindowsPath(path string) string {
	path = filepath.Clean(path)
	if strings.HasPrefix(path, `\\?\`) {
		return path
	}
	if strings.HasPrefix(path, `\\`) {
		return `\\?\UNC\` + strings.TrimPrefix(path, `\\`)
	}
	return `\\?\` + path
}

func prepareBackgroundTree(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
}

// resumeBackgroundTree lets the suspended worker run after job assignment.
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

// ownProcessTree assigns the suspended worker before it can spawn descendants,
// keeping the whole tree in the Job Object.
func ownProcessTree(pid int, workerName string) error {
	if _, err := config.DefaultPathManager().DataDir(); err != nil {
		return fmt.Errorf("resolve Devsy data directory for job object: %w", err)
	}
	jobName, err := jobNameFor(workerName)
	if err != nil {
		return err
	}
	name, err := windows.UTF16PtrFromString(jobName)
	if err != nil {
		return err
	}
	job, err := createWorkerJobObject(name)
	if err != nil {
		return err
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

func createWorkerJobObject(name *uint16) (windows.Handle, error) {
	rawHandle, _, callErr := procCreateJobObjectForWorker.Call(
		0,
		uintptr(unsafe.Pointer(name)),
	)
	job := windows.Handle(rawHandle)
	if job == 0 {
		return 0, fmt.Errorf("create job object: %w", callErr)
	}
	if errors.Is(callErr, windows.ERROR_ALREADY_EXISTS) {
		_ = windows.CloseHandle(job)
		return 0, fmt.Errorf("job object %q already exists", windows.UTF16PtrToString(name))
	}
	return job, nil
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

func terminateJobAfterWorkerExit(workerName string) (bool, error) {
	job, found, err := openWorkerJob(workerName)
	if err != nil {
		return false, err
	}
	if !found && isDetachedTaskWorkerName(workerName) {
		job, found, err = openLegacyWorkerJob(workerName)
		if err != nil {
			return false, err
		}
	}
	if !found {
		return false, nil
	}
	defer func() { _ = windows.CloseHandle(job) }()
	return terminateJobHandle(job, workerName)
}

func terminateNamedJob(jobID string) (bool, error) {
	if jobID == "" {
		return false, errors.New("worker job reference is empty")
	}
	job, found, err := openNamedJob(jobID, jobID)
	if err != nil || !found {
		return found, err
	}
	defer func() { _ = windows.CloseHandle(job) }()
	return terminateJobHandle(job, jobID)
}

func isDetachedTaskWorkerName(workerName string) bool {
	if !strings.HasPrefix(workerName, detachedTaskWorkerPrefix) {
		return false
	}
	taskID := strings.TrimPrefix(workerName, detachedTaskWorkerPrefix)
	if len(taskID) != 12 {
		return false
	}
	for _, char := range taskID {
		if !(char >= 'a' && char <= 'z') && !(char >= 'A' && char <= 'Z') &&
			!(char >= '0' && char <= '9') {
			return false
		}
	}
	return true
}

func terminateJobForPID(workerName string, pid int) (bool, error) {
	job, found, err := openWorkerJob(workerName)
	if err != nil {
		return false, err
	}
	if found {
		inJob, verifyErr := processBelongsToJob(pid, job)
		if verifyErr != nil {
			_ = windows.CloseHandle(job)
			if errors.Is(verifyErr, windows.ERROR_INVALID_PARAMETER) {
				return false, fmt.Errorf(
					"worker process %d exited before job membership was verified",
					pid,
				)
			}
			return false, verifyErr
		}
		if inJob {
			defer func() { _ = windows.CloseHandle(job) }()
			return terminateJobHandle(job, workerName)
		}
		_ = windows.CloseHandle(job)
	}
	job, found, err = openLegacyWorkerJob(workerName)
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
	jobName, err := jobNameFor(workerName)
	if err != nil {
		return 0, false, err
	}
	return openNamedJob(jobName, workerName)
}

func openLegacyWorkerJob(workerName string) (windows.Handle, bool, error) {
	return openNamedJob(workerName, workerName)
}

func openNamedJob(jobName, workerName string) (windows.Handle, bool, error) {
	name, err := windows.UTF16PtrFromString(jobName)
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
