//go:build windows

package pty

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Allocates a PTY and starts the specified command attached to it.
// See: https://docs.microsoft.com/en-us/windows/console/creating-a-pseudoconsole-session#creating-the-hosted-process
func startPty(cmd *Cmd, opt ...StartOption) (_ PTYCmd, _ Process, retErr error) {
	var opts startOptions
	for _, o := range opt {
		o(&opts)
	}

	winCmd, err := resolveCommand(cmd)
	if err != nil {
		return nil, nil, err
	}

	winPty, err := newConPty(opts.ptyOpts...)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if retErr != nil {
			_ = winPty.Close()
		}
	}()
	envBlock := processEnvBlock(cmd.Env, winPty)

	attrs, err := newConsoleAttrList(winPty.console)
	if err != nil {
		return nil, nil, err
	}
	defer attrs.Delete()

	processInfo, err := createConsoleProcess(winCmd, attrs, envBlock)
	if err != nil {
		return nil, nil, err
	}
	defer windows.CloseHandle(processInfo.Thread)
	defer windows.CloseHandle(processInfo.Process)

	process, err := os.FindProcess(int(processInfo.ProcessId))
	if err != nil {
		return nil, nil, fmt.Errorf("find process %d: %w", processInfo.ProcessId, err)
	}
	wp := &windowsProcess{
		cmdDone: make(chan any),
		proc:    process,
		pw:      winPty,
	}
	defer func() {
		if retErr != nil {
			_ = process.Kill()
		}
	}()

	if err = closeChildPipeEnds(winPty); err != nil {
		return nil, nil, err
	}
	go wp.waitInternal()
	if cmd.Context != nil {
		go wp.killOnContext(cmd.Context)
	}
	return winPty, wp, nil
}

// winCommand holds the UTF-16 arguments for CreateProcess.
type winCommand struct {
	path    *uint16
	cmdLine *uint16
	dir     *uint16
}

// resolveCommand looks up the executable and defaults cmd.Dir to the working directory.
func resolveCommand(cmd *Cmd) (winCommand, error) {
	fullPath, err := exec.LookPath(cmd.Path)
	if err != nil {
		return winCommand{}, err
	}
	pathPtr, err := windows.UTF16PtrFromString(fullPath)
	if err != nil {
		return winCommand{}, err
	}
	argsPtr, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(cmd.Args))
	if err != nil {
		return winCommand{}, err
	}
	if cmd.Dir == "" {
		cmd.Dir, err = os.Getwd()
		if err != nil {
			return winCommand{}, err
		}
	}
	dirPtr, err := windows.UTF16PtrFromString(cmd.Dir)
	if err != nil {
		return winCommand{}, err
	}
	return winCommand{path: pathPtr, cmdLine: argsPtr, dir: dirPtr}, nil
}

// newConsoleAttrList returns a process attribute list that attaches the child to console.
func newConsoleAttrList(console windows.Handle) (*windows.ProcThreadAttributeListContainer, error) {
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return nil, err
	}
	// Taken from: https://github.com/microsoft/hcsshim/blob/2314362e977aa03b3ed245a4beb12d00422af0e2/internal/winapi/process.go#L6
	err = attrs.Update(0x20016, unsafe.Pointer(console), unsafe.Sizeof(console))
	if err != nil {
		attrs.Delete()
		return nil, err
	}
	return attrs, nil
}

// processEnvBlock returns the child's UTF-16 environment block, or nil to inherit the parent's.
func processEnvBlock(env []string, pty *ptyWindows) *uint16 {
	if pty.opts.sshReq != nil {
		if env == nil {
			env = os.Environ()
		}
		env = append(env, fmt.Sprintf("SSH_TTY=%s", pty.Name()))
	}
	if env == nil {
		return nil
	}
	return createEnvBlock(addCriticalEnv(dedupEnvCase(true, env)))
}

// createConsoleProcess starts the command attached to attrs. On success the caller owns the returned handles.
func createConsoleProcess(
	cmd winCommand,
	attrs *windows.ProcThreadAttributeListContainer,
	envBlock *uint16,
) (windows.ProcessInformation, error) {
	startupInfo := &windows.StartupInfoEx{}
	startupInfo.ProcThreadAttributeList = attrs.List()
	startupInfo.StartupInfo.Flags = windows.STARTF_USESTDHANDLES
	startupInfo.StartupInfo.Cb = uint32(unsafe.Sizeof(*startupInfo))
	var processInfo windows.ProcessInformation
	err := windows.CreateProcess(
		cmd.path,
		cmd.cmdLine,
		nil,
		nil,
		false,
		// https://docs.microsoft.com/en-us/windows/win32/procthread/process-creation-flags#create_unicode_environment
		windows.CREATE_UNICODE_ENVIRONMENT|windows.EXTENDED_STARTUPINFO_PRESENT,
		envBlock,
		cmd.dir,
		&startupInfo.StartupInfo,
		&processInfo,
	)
	return processInfo, err
}

// closeChildPipeEnds closes the PTY pipe ends the child now holds; both are attempted before reporting the first error.
func closeChildPipeEnds(p *ptyWindows) error {
	errO := p.outputWrite.Close()
	p.outputWrite = nil
	errI := p.inputRead.Close()
	p.inputRead = nil
	if errO != nil {
		return errO
	}
	return errI
}

// Taken from: https://github.com/microsoft/hcsshim/blob/7fbdca16f91de8792371ba22b7305bf4ca84170a/internal/exec/exec.go#L476
func createEnvBlock(envv []string) *uint16 {
	if len(envv) == 0 {
		return &utf16.Encode([]rune("\x00\x00"))[0]
	}
	length := 0
	for _, s := range envv {
		length += len(s) + 1
	}
	length += 1

	b := make([]byte, length)
	i := 0
	for _, s := range envv {
		l := len(s)
		copy(b[i:i+l], []byte(s))
		copy(b[i+l:i+l+1], []byte{0})
		i = i + l + 1
	}
	copy(b[i:i+1], []byte{0})

	return &utf16.Encode([]rune(string(b)))[0]
}

// dedupEnvCase is dedupEnv with a case option for testing.
// If caseInsensitive is true, the case of keys is ignored.
func dedupEnvCase(caseInsensitive bool, env []string) []string {
	out := make([]string, 0, len(env))
	saw := make(map[string]int, len(env)) // key => index into out
	for _, kv := range env {
		eq := strings.Index(kv, "=")
		if eq < 0 {
			out = append(out, kv)
			continue
		}
		k := kv[:eq]
		if caseInsensitive {
			k = strings.ToLower(k)
		}
		if dupIdx, isDup := saw[k]; isDup {
			out[dupIdx] = kv
			continue
		}
		saw[k] = len(out)
		out = append(out, kv)
	}
	return out
}

// addCriticalEnv adds any critical environment variables that are required
// (or at least almost always required) on the operating system.
func addCriticalEnv(env []string) []string {
	for _, kv := range env {
		eq := strings.Index(kv, "=")
		if eq < 0 {
			continue
		}
		k := kv[:eq]
		if strings.EqualFold(k, "SYSTEMROOT") {
			return env
		}
	}
	return append(env, "SYSTEMROOT="+os.Getenv("SYSTEMROOT"))
}
