//go:build windows

package docker

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// procThreadAttributeJobList is PROC_THREAD_ATTRIBUTE_JOB_LIST (Windows 10+);
// golang.org/x/sys/windows does not export it.
const procThreadAttributeJobList = 0x0002000D

// launchDialStdio creates the child directly inside a per-connection
// kill-on-close Job Object (STARTUPINFOEX + PROC_THREAD_ATTRIBUTE_JOB_LIST),
// so no descendant can start before the job owns the tree. Any failed step
// leaves nothing running: there is deliberately no single-process fallback.
func launchDialStdio(spec launchSpec) (*launchedStdio, error) {
	cmd, err := encodeWindowsCommand(spec)
	if err != nil {
		return nil, err
	}

	var owned handleSet
	defer owned.closeAll()

	pipes, err := owned.stdioPipes()
	if err != nil {
		return nil, err
	}
	job, err := newKillOnCloseJob()
	if err != nil {
		return nil, err
	}
	owned.add(job)

	childEnds := []windows.Handle{pipes.stdinR, pipes.stdoutW, pipes.stderrW}
	attrs, err := newLaunchAttributes(job, childEnds)
	if err != nil {
		return nil, err
	}
	defer attrs.Delete()

	si := newStartupInfo(pipes, attrs)
	var pi windows.ProcessInformation
	err = windows.CreateProcess(
		cmd.app, cmd.cmdline, nil, nil, true,
		windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_UNICODE_ENVIRONMENT,
		cmd.env, nil, &si.StartupInfo, &pi,
	)
	runtime.KeepAlive(childEnds)
	if err != nil {
		return nil, winError("CreateProcessW", err)
	}

	// The child exists and already belongs to the job. Nothing below can fail.
	_ = windows.CloseHandle(pi.Thread)
	owned.release(pipes.stdinR)
	owned.release(pipes.stdoutW)
	owned.release(pipes.stderrW)
	owned.forget(job)
	owned.forget(pipes.stdinW)
	owned.forget(pipes.stdoutR)
	owned.forget(pipes.stderrR)

	return &launchedStdio{
		proc:   &jobProcess{pid: pi.ProcessId, job: job, proc: pi.Process},
		stdin:  os.NewFile(uintptr(pipes.stdinW), "dial-stdio-stdin"),
		stdout: os.NewFile(uintptr(pipes.stdoutR), "dial-stdio-stdout"),
		stderr: os.NewFile(uintptr(pipes.stderrR), "dial-stdio-stderr"),
	}, nil
}

func newStartupInfo(
	pipes childPipes,
	attrs *windows.ProcThreadAttributeListContainer,
) windows.StartupInfoEx {
	return windows.StartupInfoEx{
		StartupInfo: windows.StartupInfo{
			Cb:        uint32(unsafe.Sizeof(windows.StartupInfoEx{})),
			Flags:     windows.STARTF_USESTDHANDLES,
			StdInput:  pipes.stdinR,
			StdOutput: pipes.stdoutW,
			StdErr:    pipes.stderrW,
		},
		ProcThreadAttributeList: attrs.List(),
	}
}

// windowsCommand is the encoded CreateProcessW input.
type windowsCommand struct {
	app, cmdline, env *uint16
}

func encodeWindowsCommand(spec launchSpec) (windowsCommand, error) {
	app, err := resolveWindowsExecutable(spec.path)
	if err != nil {
		return windowsCommand{}, err
	}
	app16, err := windows.UTF16PtrFromString(app)
	if err != nil {
		return windowsCommand{}, fmt.Errorf("encode executable path: %w", err)
	}
	cmdline16, err := windows.UTF16PtrFromString(
		windows.ComposeCommandLine(append([]string{app}, spec.args...)),
	)
	if err != nil {
		return windowsCommand{}, fmt.Errorf("encode command line: %w", err)
	}
	env, err := buildEnvBlock(spec.env)
	if err != nil {
		return windowsCommand{}, err
	}
	env16 := &env[0]
	return windowsCommand{app: app16, cmdline: cmdline16, env: env16}, nil
}

// newLaunchAttributes builds the attribute list that places the child in job at
// creation and restricts handle inheritance to inherit. The returned list keeps
// the attribute payloads alive until Delete.
func newLaunchAttributes(
	job windows.Handle,
	inherit []windows.Handle,
) (*windows.ProcThreadAttributeListContainer, error) {
	attrs, err := windows.NewProcThreadAttributeList(2)
	if err != nil {
		return nil, winError("InitializeProcThreadAttributeList", err)
	}
	jobs := []windows.Handle{job}
	if err := attrs.Update(
		procThreadAttributeJobList,
		unsafe.Pointer(&jobs[0]), //nolint:gosec // Win32 attribute payload
		uintptr(len(jobs))*unsafe.Sizeof(jobs[0]),
	); err != nil {
		attrs.Delete()
		return nil, winError("UpdateProcThreadAttribute(JOB_LIST)", err)
	}
	if err := attrs.Update(
		windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST,
		unsafe.Pointer(&inherit[0]), //nolint:gosec // Win32 attribute payload
		uintptr(len(inherit))*unsafe.Sizeof(inherit[0]),
	); err != nil {
		attrs.Delete()
		return nil, winError("UpdateProcThreadAttribute(HANDLE_LIST)", err)
	}
	return attrs, nil
}

// childPipes holds the three stdio pipes: the child ends are inheritable, the
// parent ends never are.
type childPipes struct {
	stdinR, stdinW   windows.Handle
	stdoutR, stdoutW windows.Handle
	stderrR, stderrW windows.Handle
}

func (s *handleSet) stdioPipes() (childPipes, error) {
	var p childPipes
	var err error
	if p.stdinR, p.stdinW, err = s.pipe("stdin"); err != nil {
		return p, err
	}
	if p.stdoutR, p.stdoutW, err = s.pipe("stdout"); err != nil {
		return p, err
	}
	if p.stderrR, p.stderrW, err = s.pipe("stderr"); err != nil {
		return p, err
	}
	for _, h := range []windows.Handle{p.stdinR, p.stdoutW, p.stderrW} {
		if err := windows.SetHandleInformation(
			h, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT,
		); err != nil {
			return p, winError("SetHandleInformation", err)
		}
	}
	return p, nil
}

// resolveWindowsExecutable finds the executable and rejects batch scripts,
// which would need cmd.exe and its quoting rules.
func resolveWindowsExecutable(name string) (string, error) {
	found, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(found)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", found, err)
	}
	switch strings.ToLower(filepath.Ext(abs)) {
	case ".bat", ".cmd":
		return "", fmt.Errorf(
			"docker command %q is a batch script; a native executable is required", abs,
		)
	}
	return abs, nil
}

// buildEnvBlock builds a CreateProcessW Unicode environment block, sorted by
// case-insensitive name as the API requires. Errors never echo values.
func buildEnvBlock(env []string) ([]uint16, error) {
	sorted := append([]string(nil), env...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return strings.ToUpper(envName(sorted[i])) < strings.ToUpper(envName(sorted[j]))
	})

	var block []uint16
	for i, kv := range sorted {
		u, err := windows.UTF16FromString(kv)
		if err != nil {
			return nil, fmt.Errorf("environment entry %d contains a NUL byte", i)
		}
		block = append(block, u...)
	}
	if len(block) == 0 {
		block = append(block, 0)
	}
	return append(block, 0), nil
}

// envName returns the variable name; a leading '=' belongs to the name (for
// entries such as "=C:=C:\\").
func envName(kv string) string {
	start := 0
	if strings.HasPrefix(kv, "=") {
		start = 1
	}
	if idx := strings.IndexByte(kv[start:], '='); idx >= 0 {
		return kv[:start+idx]
	}
	return kv
}

func newKillOnCloseJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, winError("CreateJobObject", err)
	}
	// No breakaway flags: descendants can never leave the job.
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), //nolint:gosec // Win32 job information payload
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return 0, winError("SetInformationJobObject", err)
	}
	return job, nil
}

// winError reports the failed operation and the Windows error code only.
func winError(op string, err error) error {
	if errno, ok := errors.AsType[syscall.Errno](err); ok {
		return fmt.Errorf("%s failed with Windows error %d: %w", op, uintptr(errno), err)
	}
	return fmt.Errorf("%s failed: %w", op, err)
}

// handleSet tracks raw handles that must be closed if launch fails.
type handleSet []windows.Handle

func (s *handleSet) add(h windows.Handle) { *s = append(*s, h) }

// pipe creates a non-inheritable anonymous pipe and tracks both ends.
func (s *handleSet) pipe(name string) (r, w windows.Handle, err error) {
	if err := windows.CreatePipe(&r, &w, nil, 0); err != nil {
		return 0, 0, winError("CreatePipe("+name+")", err)
	}
	s.add(r)
	s.add(w)
	return r, w, nil
}

// forget stops tracking h because ownership moved elsewhere.
func (s *handleSet) forget(h windows.Handle) {
	for i, v := range *s {
		if v == h {
			*s = append((*s)[:i], (*s)[i+1:]...)
			return
		}
	}
}

// release closes h now and stops tracking it.
func (s *handleSet) release(h windows.Handle) {
	s.forget(h)
	_ = windows.CloseHandle(h)
}

func (s *handleSet) closeAll() {
	for _, h := range *s {
		_ = windows.CloseHandle(h)
	}
	*s = nil
}

// jobProcess owns the child process handle and the Job Object containing the
// child's whole tree.
type jobProcess struct {
	pid uint32

	mu       sync.Mutex
	job      windows.Handle
	proc     windows.Handle
	released bool // guarded by mu; once true the handles are closed
}

var _ dialProcess = (*jobProcess)(nil)

func (p *jobProcess) PID() int { return int(p.pid) }

func (p *jobProcess) Wait() error {
	p.mu.Lock()
	if p.released {
		p.mu.Unlock()
		return errors.New("wait on released process")
	}
	proc := p.proc
	p.mu.Unlock()

	event, err := windows.WaitForSingleObject(proc, windows.INFINITE)
	if err != nil {
		return winError("WaitForSingleObject", err)
	}
	if event != windows.WAIT_OBJECT_0 {
		return fmt.Errorf("WaitForSingleObject returned unexpected event %#x", event)
	}
	var code uint32
	if err := windows.GetExitCodeProcess(proc, &code); err != nil {
		return winError("GetExitCodeProcess", err)
	}
	if code != 0 {
		return exitStatusError(code)
	}
	return nil
}

// KillTree terminates every process in the job, descendants included. It holds
// the lock so Release cannot close the job underneath it.
func (p *jobProcess) KillTree() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.released {
		return nil
	}
	if err := windows.TerminateJobObject(p.job, 1); err != nil {
		return winError("TerminateJobObject", err)
	}
	return nil
}

// Release closes the job (killing any straggler through KILL_ON_JOB_CLOSE) and
// the process handle exactly once.
func (p *jobProcess) Release() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.released {
		return nil
	}
	p.released = true
	var errs []error
	if err := windows.CloseHandle(p.job); err != nil {
		errs = append(errs, winError("CloseHandle(job)", err))
	}
	if err := windows.CloseHandle(p.proc); err != nil {
		errs = append(errs, winError("CloseHandle(process)", err))
	}
	return errors.Join(errs...)
}

// exitStatusError mirrors the "exit status N" text of os/exec.ExitError.
type exitStatusError uint32

func (e exitStatusError) Error() string {
	if e >= 0x80000000 {
		return fmt.Sprintf("exit status %#x", uint32(e))
	}
	return fmt.Sprintf("exit status %d", uint32(e))
}
