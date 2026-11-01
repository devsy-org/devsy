//go:build windows

package command

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows uses a named Job Object as the launch lifetime claim.
type LaunchLease struct{}

func NewLaunchLease(_ string) (*LaunchLease, error) { return &LaunchLease{}, nil }
func (*LaunchLease) Close() error                   { return nil }
func (*LaunchLease) attach(_ *exec.Cmd)             {}
func RetainLaunchLease() error                      { return nil }

func CleanupUnpublishedLaunch(_, name string, prepared bool) error {
	if !prepared {
		return nil
	}
	_, err := terminateNamedJob(name)
	return err
}

func startOwnedBackground(
	cmd *exec.Cmd,
	name string,
	atomicOwnership bool,
	prepared func(string) error,
) (func(), error) {
	if !atomicOwnership {
		prepareBackgroundTree(cmd)
		return func() {}, cmd.Start()
	}
	return startInWorkerJob(cmd, name, prepared)
}

// Assigning the job in CreateProcess closes the crash window between creating
// a suspended process and assigning it to its recoverable ownership object.
func startInWorkerJob(
	cmd *exec.Cmd,
	workerName string,
	prepared func(string) error,
) (release func(), err error) {
	if err := validateAtomicWorker(cmd); err != nil {
		return nil, err
	}
	job, jobID, err := createLaunchJob(workerName)
	if err != nil {
		return nil, err
	}
	closeJob := func() { _ = windows.CloseHandle(job) }
	defer func() {
		if err != nil {
			_, cleanupErr := terminateJobHandle(job, workerName)
			err = errors.Join(err, cleanupErr)
			closeJob()
		}
	}()
	si, closeStartup, err := workerStartupInfo(cmd, job)
	if err != nil {
		return nil, err
	}
	defer closeStartup()
	if err := notifyLaunchPrepared(prepared, jobID); err != nil {
		return nil, err
	}
	pi, err := createOwnedWorker(cmd, si)
	if err != nil {
		return nil, err
	}
	defer func() { _ = windows.CloseHandle(pi.Thread); _ = windows.CloseHandle(pi.Process) }()
	if err := retainWorkerJob(cmd, job, pi); err != nil {
		return nil, err
	}
	return closeJob, nil
}

func validateAtomicWorker(cmd *exec.Cmd) error {
	if cmd.Err != nil {
		return cmd.Err
	}
	if cmd.SysProcAttr != nil || cmd.Process != nil || cmd.Cancel != nil || cmd.WaitDelay != 0 {
		return errors.New(
			"atomic worker launch requires an unstarted command without process attributes or cancellation",
		)
	}
	return nil
}

func createLaunchJob(workerName string) (windows.Handle, string, error) {
	jobName, err := jobNameFor(workerName)
	if err != nil {
		return 0, "", err
	}
	name, err := windows.UTF16PtrFromString(jobName)
	if err != nil {
		return 0, "", err
	}
	job, err := createWorkerJobObject(name)
	if err != nil {
		return 0, "", err
	}
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		_ = windows.CloseHandle(job)
		return 0, "", err
	}
	return job, jobName, nil
}

func workerStartupInfo(cmd *exec.Cmd, job windows.Handle) (*windows.StartupInfoEx, func(), error) {
	attributes, err := windows.NewProcThreadAttributeList(2)
	if err != nil {
		return nil, nil, err
	}
	const procThreadAttributeJobList = 0x0002000d
	if err := attributes.Update(
		procThreadAttributeJobList,
		unsafe.Pointer(&job),
		unsafe.Sizeof(job),
	); err != nil {
		attributes.Delete()
		return nil, nil, fmt.Errorf("configure creation-time worker job: %w", err)
	}
	handles, closeHandles, err := workerStandardHandles(cmd)
	if err != nil {
		attributes.Delete()
		return nil, nil, err
	}
	cleanup := func() { closeHandles(); attributes.Delete() }
	if err := attributes.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST,
		unsafe.Pointer(&handles[0]), uintptr(len(handles))*unsafe.Sizeof(handles[0])); err != nil {
		cleanup()
		return nil, nil, err
	}
	si := &windows.StartupInfoEx{
		StartupInfo: windows.StartupInfo{
			Cb: uint32(unsafe.Sizeof(windows.StartupInfoEx{})), Flags: windows.STARTF_USESTDHANDLES,
			StdInput: handles[0], StdOutput: handles[1], StdErr: handles[2],
		},
		ProcThreadAttributeList: attributes.List(),
	}
	return si, cleanup, nil
}

func notifyLaunchPrepared(prepared func(string) error, jobID string) error {
	if prepared == nil {
		return nil
	}
	return prepared(jobID)
}

func createOwnedWorker(
	cmd *exec.Cmd,
	si *windows.StartupInfoEx,
) (*windows.ProcessInformation, error) {
	application, err := windows.UTF16PtrFromString(cmd.Path)
	if err != nil {
		return nil, err
	}
	args := cmd.Args
	if len(args) == 0 {
		args = []string{cmd.Path}
	}
	commandLine, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(args))
	if err != nil {
		return nil, err
	}
	var directory *uint16
	if cmd.Dir != "" {
		directory, err = windows.UTF16PtrFromString(cmd.Dir)
		if err != nil {
			return nil, err
		}
	}
	block, err := workerEnvironment(cmd.Environ())
	if err != nil {
		return nil, err
	}
	pi := new(windows.ProcessInformation)
	err = windows.CreateProcess(application, commandLine, nil, nil, true,
		windows.CREATE_UNICODE_ENVIRONMENT|windows.EXTENDED_STARTUPINFO_PRESENT,
		&block[0], directory, &si.StartupInfo, pi)
	if err != nil {
		return nil, err
	}
	return pi, nil
}

func retainWorkerJob(cmd *exec.Cmd, job windows.Handle, pi *windows.ProcessInformation) error {
	// Until the worker holds this handle, launcher death closes the last handle
	// and the kernel kills the child. Afterwards recovery can open the named job.
	var childJob windows.Handle
	if err := windows.DuplicateHandle(windows.CurrentProcess(), job, pi.Process, &childJob,
		0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		return err
	}
	var err error
	cmd.Process, err = os.FindProcess(int(pi.ProcessId))
	return err
}

func workerStandardHandles(cmd *exec.Cmd) ([]windows.Handle, func(), error) {
	handles := make([]windows.Handle, 0, 3)
	closeHandles := func() {
		for _, h := range handles {
			_ = windows.CloseHandle(h)
		}
	}
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return nil, closeHandles, err
	}
	defer func() { _ = null.Close() }()
	for _, stream := range []any{cmd.Stdin, cmd.Stdout, cmd.Stderr} {
		file := null
		if stream != nil {
			var ok bool
			file, ok = stream.(*os.File)
			if !ok {
				closeHandles()
				return nil, func() {}, errors.New("atomic worker streams must be files")
			}
		}
		var inherited windows.Handle
		if err := windows.DuplicateHandle(
			windows.CurrentProcess(),
			windows.Handle(file.Fd()),
			windows.CurrentProcess(),
			&inherited,
			0,
			true,
			windows.DUPLICATE_SAME_ACCESS,
		); err != nil {
			closeHandles()
			return nil, func() {}, err
		}
		handles = append(handles, inherited)
	}
	return handles, closeHandles, nil
}

func workerEnvironment(env []string) ([]uint16, error) {
	env = slices.Clone(env)
	slices.SortFunc(
		env,
		func(a, b string) int { return strings.Compare(strings.ToUpper(a), strings.ToUpper(b)) },
	)
	var block []uint16
	for _, entry := range env {
		encoded, err := windows.UTF16FromString(entry)
		if err != nil {
			return nil, err
		}
		block = append(block, encoded...)
	}
	if len(block) == 0 {
		block = append(block, 0)
	}
	return append(block, 0), nil
}
