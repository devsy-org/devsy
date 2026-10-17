//go:build windows

package server

import (
	"errors"
	"os/exec"
)

func prepareExecIdentity(_ *exec.Cmd, spec string) (bool, error) {
	_, handled, err := parseRequestedExecIdentity(spec)
	if err != nil || !handled {
		return handled, err
	}
	return true, errors.New("numeric execution identities are not supported on Windows")
}
