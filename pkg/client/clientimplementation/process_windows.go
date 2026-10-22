//go:build windows

package clientimplementation

import (
	"errors"

	"golang.org/x/sys/windows"
)

func isInvalidProcessParameter(err error) bool {
	return errors.Is(err, windows.ERROR_INVALID_PARAMETER)
}
