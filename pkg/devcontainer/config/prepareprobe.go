//go:build !windows

package config

import (
	"fmt"
	"maps"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"syscall"
)

func PrepareCmdUser(cmd *exec.Cmd, userName string) error {
	// execute as user
	u, err := user.Lookup(userName)
	if err != nil {
		return fmt.Errorf("lookup user %s: %w", userName, err)
	}
	uid, _ := strconv.ParseUint(u.Uid, 10, 32)
	gid, _ := strconv.ParseUint(u.Gid, 10, 32)
	cmd.Env = patchEnvVars(cmd.Environ(), map[string]string{
		"HOME":    u.HomeDir,
		"USER":    u.Username,
		"LOGNAME": u.Username,
	})

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{
			Uid: uint32(uid),
			Gid: uint32(gid),
		},
	}

	return nil
}

func patchEnvVars(env []string, patches map[string]string) []string {
	newEnv := map[string]string{}
	for _, v := range env {
		name, value, ok := strings.Cut(v, "=")
		if !ok || name == "" {
			continue
		}
		newEnv[name] = value
	}

	// apply patches
	maps.Copy(newEnv, patches)

	retEnv := []string{}
	for k, v := range newEnv {
		retEnv = append(retEnv, fmt.Sprintf("%s=%s", k, v))
	}

	return retEnv
}
