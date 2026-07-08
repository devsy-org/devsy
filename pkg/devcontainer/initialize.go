package devcontainer

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
)

// initializeCommand carries the shared execution state for the initializeCommand
// hook: the host shell, working directory, and extra environment.
type initializeCommand struct {
	shell           []string
	workspaceFolder string
	extraEnv        []string
}

// runInitializeCommand executes the devcontainer.json initializeCommand hook on
// the host. Named sub-commands run concurrently; their errors are collected and
// returned together.
func runInitializeCommand(
	workspaceFolder string,
	conf *config.DevContainerConfig,
	extraEnv []string,
) error {
	if len(conf.InitializeCommand) == 0 {
		return nil
	}

	init := &initializeCommand{
		shell:           hostShell(),
		workspaceFolder: workspaceFolder,
		extraEnv:        extraEnv,
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	for name, cmd := range conf.InitializeCommand {
		wg.Go(func() {
			if err := init.run(name, cmd); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		})
	}
	wg.Wait()

	return errors.Join(errs...)
}

// hostShell returns the shell invocation used to run string-form commands.
// initializeCommand runs on the host; on Windows sh may not be on PATH, so we
// fall back to the default command interpreter.
func hostShell() []string {
	if runtime.GOOS != "windows" {
		return []string{"sh", "-c"}
	}
	if comSpec := os.Getenv("COMSPEC"); comSpec != "" {
		return []string{comSpec, "/c"}
	}
	return []string{"cmd.exe", "/c"}
}

// run executes a single named sub-command. A single-element command is run as a
// shell string; a multi-element command is executed argv-style.
func (c *initializeCommand) run(name string, cmd []string) error {
	args := cmd
	if len(cmd) == 1 {
		args = append(append([]string{}, c.shell...), cmd[0])
	}

	log.Infof(
		"Running initializeCommand %q from devcontainer.json: %q",
		name,
		strings.Join(args, " "),
	)

	stdout := log.Writer(log.LevelInfo)
	stderr := log.Writer(log.LevelError)
	defer func() { _ = stdout.Close() }()
	defer func() { _ = stderr.Close() }()

	// args come from devcontainer.json initializeCommand, a trusted local config.
	command := exec.Command(args[0], args[1:]...) //nolint:gosec // G204
	command.Dir = c.workspaceFolder
	command.Env = append(command.Environ(), c.extraEnv...)
	command.Stdout = stdout
	command.Stderr = stderr

	if err := command.Run(); err != nil {
		return fmt.Errorf("initializeCommand %q failed: %w", name, err)
	}
	return nil
}
