//go:build windows

package server

import (
	"errors"
	"os/exec"
	"os/user"
)

func prepareExecIdentity(_ *exec.Cmd, spec string) (bool, error) {
	_, handled, err := parseRequestedExecIdentity(spec)
	if err != nil || !handled {
		return handled, err
	}
	return true, errors.New("numeric execution identities are not supported on Windows")
}

func prepareExecIdentityForAccount(cmd *exec.Cmd, spec string, _ *user.User) (bool, error) {
	return prepareExecIdentity(cmd, spec)
}
