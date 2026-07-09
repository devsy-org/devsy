// Package flatpak handles running the CLI outside the Flatpak sandbox.
package flatpak

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/devsy-org/devsy/pkg/log"
)

var flatpakInfoPath = "/.flatpak-info"

const flatpakSpawn = "flatpak-spawn"

func InSandbox() bool {
	_, err := os.Stat(flatpakInfoPath)
	return err == nil
}

func HostBinaryPath() string {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(os.Getenv("HOME"), ".local", "share")
	}
	return filepath.Join(dataHome, "devsy", "devsy")
}

// ReexecOnHost re-executes the current process on the host via `flatpak-spawn --host`.
func ReexecOnHost() (bool, error) {
	if !InSandbox() {
		return false, nil
	}

	hostBinary := HostBinaryPath()
	if _, err := os.Stat(hostBinary); err != nil {
		return false, fmt.Errorf(
			"host devsy binary not found at %s; the Flatpak launcher should sync it on startup: %w",
			hostBinary, err,
		)
	}

	spawnArgs := append([]string{"--host", hostBinary}, os.Args[1:]...)
	log.Debugf("re-executing on host via flatpak-spawn: args=%v", os.Args[1:])

	//nolint:gosec
	cmd := exec.Command(flatpakSpawn, spawnArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()

	if err := cmd.Run(); err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return true, exitErr
		}
		return false, err
	}

	return true, nil
}
