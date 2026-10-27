//go:build windows

package command

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestNativeT0ObserveEditorLifetime(t *testing.T) {
	taskID := os.Getenv("DEVSY_T0_TASK_ID")
	phase := os.Getenv("DEVSY_T0_PHASE")
	workerPID, err := strconv.Atoi(os.Getenv("DEVSY_T0_WORKER_PID"))
	if err != nil || workerPID <= 0 {
		t.Fatalf("invalid worker PID: %v", err)
	}
	editorPID, err := strconv.Atoi(os.Getenv("DEVSY_T0_EDITOR_PID"))
	if err != nil || editorPID <= 0 {
		t.Fatalf("invalid editor PID: %v", err)
	}
	if taskID == "" || phase == "" {
		t.Fatal("task ID and phase are required")
	}

	worker, err := windows.OpenProcess(
		windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE,
		false,
		uint32(workerPID),
	)
	if err != nil {
		t.Fatalf("open worker process: %v", err)
	}
	defer func() { _ = windows.CloseHandle(worker) }()
	workerImage, err := processImageName(worker, workerPID)
	if err != nil {
		t.Fatalf("read worker image: %v", err)
	}
	workerBase := filepath.Base(workerImage)
	if !strings.EqualFold(workerBase, "devsy.exe") {
		t.Fatalf("worker process is not Devsy: %s", workerBase)
	}

	editor, err := windows.OpenProcess(
		windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE,
		false,
		uint32(editorPID),
	)
	if err != nil {
		t.Fatalf("open editor process: %v", err)
	}
	defer func() { _ = windows.CloseHandle(editor) }()
	editorImage, err := processImageName(editor, editorPID)
	if err != nil {
		t.Fatalf("read editor image: %v", err)
	}
	editorBase := filepath.Base(editorImage)
	if !strings.EqualFold(editorBase, "VSCodium.exe") {
		t.Fatalf("editor process is not VSCodium: %s", editorBase)
	}

	var workerCreated, exited, workerKernel, workerUser windows.Filetime
	if err := windows.GetProcessTimes(
		worker,
		&workerCreated,
		&exited,
		&workerKernel,
		&workerUser,
	); err != nil {
		t.Fatalf("read worker creation time: %v", err)
	}
	var editorCreated, editorExited, editorKernel, editorUser windows.Filetime
	if err := windows.GetProcessTimes(
		editor,
		&editorCreated,
		&editorExited,
		&editorKernel,
		&editorUser,
	); err != nil {
		t.Fatalf("read editor creation time: %v", err)
	}

	workerName := detachedTaskWorkerPrefix + taskID
	jobName, err := jobNameFor(workerName)
	if err != nil {
		t.Fatalf("resolve detached worker job name: %v", err)
	}
	job, err := openT0QueryJob(jobName)
	if err != nil {
		t.Fatalf("open detached worker job: %v", err)
	}
	workerInJob, err := processIsInT0Job(uint32(workerPID), job)
	if err != nil {
		_ = windows.CloseHandle(job)
		t.Fatalf("query worker job membership: %v", err)
	}
	editorInJob, err := processIsInT0Job(uint32(editorPID), job)
	if err != nil {
		_ = windows.CloseHandle(job)
		t.Fatalf("query editor job membership: %v", err)
	}
	var accounting jobAccountingInfo
	if err := windows.QueryInformationJobObject(
		job,
		windows.JobObjectBasicAccountingInformation,
		uintptr(unsafe.Pointer(&accounting)),
		uint32(unsafe.Sizeof(accounting)),
		nil,
	); err != nil {
		_ = windows.CloseHandle(job)
		t.Fatalf("query job process count: %v", err)
	}
	var processList struct {
		assigned uint32
		listed   uint32
		pids     [1024]uintptr
	}
	if err := windows.QueryInformationJobObject(
		job,
		windows.JobObjectBasicProcessIdList,
		uintptr(unsafe.Pointer(&processList)),
		uint32(unsafe.Sizeof(processList)),
		nil,
	); err != nil {
		_ = windows.CloseHandle(job)
		t.Fatalf("query job process IDs: %v", err)
	}
	if processList.listed > uint32(len(processList.pids)) {
		_ = windows.CloseHandle(job)
		t.Fatalf(
			"job process ID list exceeded observer capacity: assigned=%d listed=%d",
			processList.assigned,
			processList.listed,
		)
	}
	jobPIDs := make([]uintptr, processList.listed)
	copy(jobPIDs, processList.pids[:processList.listed])
	if err := windows.CloseHandle(job); err != nil {
		t.Fatalf("close observer job handle: %v", err)
	}

	t.Logf(
		"T0 phase=%s task=%s workerPID=%d workerImage=%s workerCreateTime=%d workerInJob=%t editorPID=%d editorImage=%s editorCreateTime=%d editorInJob=%t jobActiveProcesses=%d jobPIDs=%v observerJobHandlesClosed=true",
		phase,
		taskID,
		workerPID,
		workerBase,
		workerCreated.Nanoseconds(),
		workerInJob,
		editorPID,
		editorBase,
		editorCreated.Nanoseconds(),
		editorInJob,
		accounting.activeProcesses,
		jobPIDs,
	)

	observedAt := time.Now()
	result, err := windows.WaitForSingleObject(worker, uint32((5 * time.Minute).Milliseconds()))
	if err != nil {
		t.Fatalf("wait for worker exit: %v", err)
	}
	if result != windows.WAIT_OBJECT_0 {
		t.Fatal("worker did not exit within five minutes")
	}
	result, err = windows.WaitForSingleObject(editor, 0)
	if err != nil {
		t.Fatalf("check editor after worker exit: %v", err)
	}
	editorAlive := result == uint32(windows.WAIT_TIMEOUT)
	t.Logf(
		"T0 phase=%s task=%s workerExited=true workerExitAfterObserverMs=%d editorPID=%d editorAliveAfterWorkerExit=%t",
		phase,
		taskID,
		time.Since(observedAt).Milliseconds(),
		editorPID,
		editorAlive,
	)
}

func openT0QueryJob(name string) (windows.Handle, error) {
	namePointer, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	handle, _, callErr := procOpenJobObjectW.Call(
		uintptr(jobObjectQueryAccess),
		0,
		uintptr(unsafe.Pointer(namePointer)),
	)
	if handle == 0 {
		if callErr != windows.ERROR_SUCCESS {
			return 0, callErr
		}
		return 0, windows.ERROR_INVALID_HANDLE
	}
	return windows.Handle(handle), nil
}

func processIsInT0Job(pid uint32, job windows.Handle) (bool, error) {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false, err
	}
	defer func() { _ = windows.CloseHandle(process) }()
	var inJob int32
	result, _, callErr := procIsProcessInJobForTermination.Call(
		uintptr(process),
		uintptr(job),
		uintptr(unsafe.Pointer(&inJob)),
	)
	if result == 0 {
		if callErr != windows.ERROR_SUCCESS {
			return false, callErr
		}
		return false, windows.ERROR_INVALID_FUNCTION
	}
	return inJob != 0, nil
}
