//go:build unix && !linux && !darwin

package command

import "errors"

func processTreeIdentity(_ int) (string, error) {
	return "", errors.New("process tree identity is implemented on Linux, macOS, and Windows")
}

func processGroupMatchesIdentity(_ int, _ string) (bool, error) {
	return false, errors.New("process tree identity is unsupported on this platform")
}
