//go:build !linux && !windows

package main

import (
	"context"
	"fmt"
	"runtime"
)

func runtimeGOOS() string               { return runtime.GOOS }
func platformDefaultPodmanPath() string { return "" }
func platformSetup(_ context.Context, _ Dependencies, _ SetupConfig) error {
	return fmt.Errorf("podman CI setup is not implemented for %s", runtime.GOOS)
}
