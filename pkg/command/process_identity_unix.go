//go:build unix && !linux && !darwin

package command

import "errors"

const strongProcessIdentitySupported = false

func processTreeIdentity(_ int) (string, error) {
	return "", nil
}

func processGroupMatchesIdentity(_ int, _ string) (bool, error) {
	return false, errors.New("process tree identity is unsupported on this platform")
}
