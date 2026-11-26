//go:build windows

package clientimplementation

import (
	"errors"
	"io/fs"

	"golang.org/x/sys/windows"
)

func isInvalidProcessParameter(err error) bool {
	var pathErr *fs.PathError
	return errors.As(err, &pathErr) &&
		pathErr.Op == "fork/exec" &&
		errors.Is(pathErr.Err, windows.ERROR_INVALID_PARAMETER)
}
