//go:build !windows

package terminal

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"

	dockerterm "github.com/moby/term"
	"golang.org/x/sys/unix"
)

var ErrNotAttached = errors.New("stdin and stdout must be attached to the foreground terminal")

var isForegroundProcessGroup = func(foreground, process int) bool {
	return foreground == process
}

func Attached(stdin, stdout *os.File) error {
	if stdin == nil || stdout == nil || !dockerterm.IsTerminal(stdin.Fd()) ||
		!dockerterm.IsTerminal(stdout.Fd()) {
		return ErrNotAttached
	}
	var foreground int32
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		stdin.Fd(),
		uintptr(unix.TIOCGPGRP),
		uintptr(unsafe.Pointer(&foreground)),
	)
	if errno != 0 {
		return fmt.Errorf("get terminal foreground process group: %w", errno)
	}
	if !isForegroundProcessGroup(int(foreground), unix.Getpgrp()) {
		return ErrNotAttached
	}
	return nil
}
