//go:build !windows

package main

import (
	"context"
	"fmt"
	"runtime"
)

func platformInstall(_ context.Context, _ Dependencies, _ InstallConfig) error {
	return fmt.Errorf("podman MSI install is only supported on Windows, not %s", runtime.GOOS)
}
