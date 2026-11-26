//go:build !windows

package terminal

import (
	"errors"
	"fmt"
	"os"

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
	foreground, err := unix.IoctlGetInt(int(stdin.Fd()), unix.TIOCGPGRP)
	if err != nil {
		return fmt.Errorf("get terminal foreground process group: %w", err)
	}
	if !isForegroundProcessGroup(int(foreground), unix.Getpgrp()) {
		return ErrNotAttached
	}
	return nil
}
