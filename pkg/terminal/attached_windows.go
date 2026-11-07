//go:build windows

package terminal

import (
	"errors"
	"os"

	dockerterm "github.com/moby/term"
)

var ErrNotAttached = errors.New("stdin and stdout must be attached to the console")

func Attached(stdin, stdout *os.File) error {
	if stdin == nil || stdout == nil || !dockerterm.IsTerminal(stdin.Fd()) ||
		!dockerterm.IsTerminal(stdout.Fd()) {
		return ErrNotAttached
	}
	return nil
}
