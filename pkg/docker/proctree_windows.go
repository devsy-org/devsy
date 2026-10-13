//go:build windows

package docker

import (
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processTree owns the child and its descendants through a kill-on-close Job
// Object, so an ssh.exe spawned by docker.exe dies with it. If the job cannot
// be set up it degrades to killing the single process.
type processTree struct {
	cmd         *exec.Cmd
	job         windows.Handle
	releaseOnce sync.Once
}

func attachProcessTree(cmd *exec.Cmd) *processTree {
	t := &processTree{cmd: cmd}

	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return t
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return t
	}

	proc, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		_ = windows.CloseHandle(job)
		return t
	}
	defer func() { _ = windows.CloseHandle(proc) }()

	if err := windows.AssignProcessToJobObject(job, proc); err != nil {
		_ = windows.CloseHandle(job)
		return t
	}
	t.job = job
	return t
}

func (t *processTree) kill() error {
	if t.job != 0 {
		if err := windows.TerminateJobObject(t.job, 1); err == nil {
			return nil
		}
	}
	return killProcessGroup(t.cmd)
}

func (t *processTree) release() {
	t.releaseOnce.Do(func() {
		if t.job != 0 {
			_ = windows.CloseHandle(t.job)
		}
	})
}
