package main

import (
	"context"
	"os/exec"
	"syscall"
)

const podmanSearchRoot = `C:\`

func platformInstall(ctx context.Context, deps Dependencies, cfg InstallConfig) error {
	return installPodmanMSI(ctx, deps, cfg, podmanSearchRoot)
}

func setCmdLine(cmd *exec.Cmd, line string) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: line}
	return nil
}
