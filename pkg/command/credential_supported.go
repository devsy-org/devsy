//go:build linux || darwin || unix

package command

import (
	"fmt"
	"os/exec"
	"os/user"
	"strconv"
	"syscall"
)

func ForUser(cmd *exec.Cmd, userName string) error {
	// Look up the user's UID and GID
	u, err := user.Lookup(userName)
	if err != nil {
		return fmt.Errorf("failed to look up user %s: %w", userName, err)
	}

	uid, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return fmt.Errorf("invalid UID %s: %w", u.Uid, err)
	}

	gid, err := strconv.ParseUint(u.Gid, 10, 32)
	if err != nil {
		return fmt.Errorf("invalid GID %s: %w", u.Gid, err)
	}

	// Set the user cmd should run as
	cmd.SysProcAttr = &syscall.SysProcAttr{}
	cmd.SysProcAttr.Credential = &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}

	return nil
}
