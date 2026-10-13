//go:build !windows

package docker

import "os/exec"

type processTree struct {
	cmd *exec.Cmd
}

func attachProcessTree(cmd *exec.Cmd) *processTree {
	return &processTree{cmd: cmd}
}

func (t *processTree) kill() error {
	return killProcessGroup(t.cmd)
}

func (t *processTree) release() {}
