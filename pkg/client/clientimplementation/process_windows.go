//go:build windows

package clientimplementation

import (
	"errors"
	"io/fs"

	"golang.org/x/sys/windows"
)

// isInvalidProcessParameter reports only process-creation failures, which os/exec wraps as
// "fork/exec"; the same errno from an output writer after the process started is not one.
func isInvalidProcessParameter(err error) bool {
	var pathErr *fs.PathError
	return errors.As(err, &pathErr) &&
		pathErr.Op == "fork/exec" &&
		errors.Is(pathErr.Err, windows.ERROR_INVALID_PARAMETER)
}
