package flatpak

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/pkg/log"
)

var flatpakInfoPath = "/.flatpak-info"

const (
	flatpakSpawn    = "flatpak-spawn"
	sandboxOnlyRoot = "/app"
)

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

func hostSpawnDirectory(cwd, home string) string {
	if cwd == "" || cwd == sandboxOnlyRoot || strings.HasPrefix(cwd, sandboxOnlyRoot+"/") {
		return home
	}
	return cwd
}

func buildSpawnArgs(hostBinary, cwd, home string, args []string) []string {
	spawnArgs := []string{}
	if dir := hostSpawnDirectory(cwd, home); dir != "" {
		spawnArgs = append(spawnArgs, "--directory="+dir)
	}
	spawnArgs = append(spawnArgs, "--host", hostBinary)
	return append(spawnArgs, args...)
}

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

	cwd, _ := os.Getwd()
	spawnArgs := buildSpawnArgs(hostBinary, cwd, os.Getenv("HOME"), os.Args[1:])
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
