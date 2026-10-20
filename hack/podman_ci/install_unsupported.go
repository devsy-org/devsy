//go:build !windows

package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
)

func platformInstall(_ context.Context, _ Dependencies, _ InstallConfig) error {
	return fmt.Errorf("podman MSI install is only supported on Windows, not %s", runtime.GOOS)
}

func setCmdLine(_ *exec.Cmd, _ string) error {
	return errors.New("verbatim command lines are only supported on Windows")
}
