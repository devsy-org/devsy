//go:build linux

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	commandCat              = "cat"
	commandInfo             = "info"
	commandRun              = "run"
	commandTrue             = "true"
	commandSudo             = "sudo"
	commandSystemctl        = "systemctl"
	commandNoPager          = "--no-pager"
	commandRemote           = "--remote"
	commandURL              = "--url"
	unitPodmanSocket        = "podman.socket"
	linuxDiagnosticsReserve = 40 * time.Second
)

func runtimeGOOS() string               { return goosLinux }
func platformDefaultPodmanPath() string { return "/usr/local/bin/podman" }

func platformSetup(ctx context.Context, deps Dependencies, cfg SetupConfig) error {
	budget, err := NewBudget(cfg.BootstrapTimeout, deps.Clock)
	if err != nil {
		return err
	}
	deps.Budget = budget
	if err := installPrivilegedFile(
		ctx,
		deps,
		"/etc/containers/containers.conf.d/99-devsy-ci.conf",
		containersConfig(cfg.CrunPath),
	); err != nil {
		return err
	}
	if err := configureAppArmor(ctx, deps); err != nil {
		return err
	}
	deps.Log.Printf("Podman executable: %s", cfg.PodmanPath)
	if err := runLinux(
		ctx,
		deps,
		CommandSpec{Path: cfg.PodmanPath, Args: []string{"--version"}, Timeout: 30 * time.Second},
	); err != nil {
		return err
	}
	if err := runLinux(
		ctx,
		deps,
		CommandSpec{Path: cfg.CrunPath, Args: []string{"--version"}, Timeout: 30 * time.Second},
	); err != nil {
		return err
	}
	logSystemDiagnostics(ctx, deps)
	if cfg.Mode == ModeRootful {
		return setupLinuxRootful(ctx, deps, cfg)
	}
	return setupLinuxRootless(ctx, deps, cfg)
}

func logSystemDiagnostics(ctx context.Context, deps Dependencies) {
	for _, spec := range []struct {
		path string
		args []string
	}{{commandCat, []string{"/etc/os-release"}}, {"uname", []string{"-a"}}} {
		if err := runLinux(
			ctx,
			deps,
			CommandSpec{Path: spec.path, Args: spec.args, Timeout: 10 * time.Second},
		); err != nil {
			deps.Log.Printf("[podman-linux] diagnostic failed: %v", err)
		}
	}
}

func setupLinuxRootless(ctx context.Context, deps Dependencies, cfg SetupConfig) error {
	path, err := runtimePath(ctx, deps, cfg, false)
	if err != nil {
		return err
	}
	deps.Log.Printf("Podman OCI runtime: %s", path)
	if err := runLinux(
		ctx,
		deps,
		CommandSpec{Path: cfg.PodmanPath, Args: []string{commandInfo}, Timeout: 60 * time.Second},
	); err != nil {
		return err
	}
	return runLinux(
		ctx,
		deps,
		CommandSpec{
			Path:    cfg.PodmanPath,
			Args:    []string{commandRun, "--rm", busyboxImage, commandTrue},
			Timeout: 120 * time.Second,
		},
	)
}

func setupLinuxRootful(ctx context.Context, deps Dependencies, cfg SetupConfig) error {
	if err := configureRootfulService(ctx, deps, cfg); err != nil {
		return err
	}
	if !waitForRootfulAPI(ctx, deps, cfg) {
		writeRootfulDiagnostics(ctx, deps)
		return fmt.Errorf("podman service did not become ready within 30s")
	}
	return verifyRootfulRuntime(ctx, deps, cfg)
}

func configureRootfulService(ctx context.Context, deps Dependencies, cfg SetupConfig) error {
	service := "[Service]\nExecStart=\nExecStart=" + strconv.Quote(
		cfg.PodmanPath,
	) + " --log-level=info system service --time=0\n"
	if err := installPrivilegedFile(
		ctx,
		deps,
		"/etc/systemd/system/podman.service.d/10-devsy-ci.conf",
		service,
	); err != nil {
		return err
	}
	if err := runLinux(
		ctx,
		deps,
		CommandSpec{
			Path:    commandSudo,
			Args:    []string{commandSystemctl, "daemon-reload"},
			Timeout: 30 * time.Second,
		},
	); err != nil {
		return err
	}
	if err := runLinux(
		ctx,
		deps,
		CommandSpec{
			Path:    commandSudo,
			Args:    []string{commandSystemctl, "enable", "--now", unitPodmanSocket},
			Timeout: 60 * time.Second,
		},
	); err != nil {
		return err
	}
	return nil
}

func waitForRootfulAPI(ctx context.Context, deps Dependencies, cfg SetupConfig) bool {
	readinessStarted := deps.Clock.Now()
	for {
		readinessRemaining := 30*time.Second - deps.Clock.Now().Sub(readinessStarted)
		budgetRemaining := deps.Budget.Remaining(linuxDiagnosticsReserve)
		if readinessRemaining <= 0 || budgetRemaining <= 0 {
			return false
		}
		result := runLinuxCommand(
			ctx,
			CommandSpec{
				Path: commandSudo,
				Args: []string{
					cfg.PodmanPath,
					commandRemote,
					commandURL,
					rootfulSocket,
					commandInfo,
				},
				Timeout: minLinuxDuration(5*time.Second, readinessRemaining),
			},
			deps,
			linuxDiagnosticsReserve,
		)
		if result.Success() {
			return true
		}
		deps.Clock.Sleep(minLinuxDuration(
			time.Second,
			30*time.Second-deps.Clock.Now().Sub(readinessStarted),
			deps.Budget.Remaining(linuxDiagnosticsReserve),
		))
	}
}

func writeRootfulDiagnostics(ctx context.Context, deps Dependencies) {
	for _, args := range [][]string{
		{commandSystemctl, "status", unitPodmanSocket, commandNoPager},
		{commandSystemctl, "status", "podman.service", commandNoPager},
		{"journalctl", "-u", unitPodmanSocket, commandNoPager, "-n", "100"},
		{"journalctl", "-u", "podman.service", commandNoPager, "-n", "100"},
	} {
		if err := runLinux(
			ctx,
			deps,
			CommandSpec{Path: commandSudo, Args: args, Timeout: 10 * time.Second},
		); err != nil {
			deps.Log.Printf("[podman-linux] diagnostic failed: %v", err)
		}
	}
}

func verifyRootfulRuntime(ctx context.Context, deps Dependencies, cfg SetupConfig) error {
	path, err := runtimePath(ctx, deps, cfg, true)
	if err != nil {
		return err
	}
	deps.Log.Printf("Podman OCI runtime: %s", path)
	if cfg.GitHubEnvPath == "" {
		return fmt.Errorf("GitHub environment file path is required for rootful setup")
	}
	files := deps.Files
	if files == nil {
		files = osFileSystem{}
	}
	f, err := files.OpenAppend(cfg.GitHubEnvPath)
	if err != nil {
		return fmt.Errorf("append DOCKER_HOST to GITHUB_ENV: %w", err)
	}
	_, writeErr := io.WriteString(f, "DOCKER_HOST="+rootfulSocket+"\n")
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := runLinux(
		ctx,
		deps,
		CommandSpec{
			Path: commandSudo,
			Args: []string{
				cfg.PodmanPath,
				commandRemote,
				commandURL,
				rootfulSocket,
				commandInfo,
			},
			Timeout: 60 * time.Second,
		},
	); err != nil {
		return err
	}
	return runLinux(
		ctx,
		deps,
		CommandSpec{
			Path: commandSudo,
			Args: []string{
				cfg.PodmanPath,
				commandRemote,
				commandURL,
				rootfulSocket,
				commandRun,
				"--rm",
				busyboxImage,
				commandTrue,
			},
			Timeout: 120 * time.Second,
		},
	)
}

func runtimePath(
	ctx context.Context,
	deps Dependencies,
	cfg SetupConfig,
	rootful bool,
) (string, error) {
	path := cfg.PodmanPath
	args := []string{commandInfo, "--format", "{{.Host.OCIRuntime.Path}}"}
	if rootful {
		path = commandSudo
		args = append([]string{cfg.PodmanPath, commandRemote, commandURL, rootfulSocket}, args...)
	}
	result := runLinuxCommand(
		ctx,
		CommandSpec{Path: path, Args: args, Timeout: 30 * time.Second},
		deps,
		0,
	)
	if !result.Success() {
		return "", resultError(CommandSpec{Path: path, Args: args}, result)
	}
	actual := strings.TrimSpace(result.Stdout)
	if actual != cfg.CrunPath {
		return actual, fmt.Errorf(
			"::error::Unexpected Podman OCI runtime: %s; expected: %s",
			actual,
			cfg.CrunPath,
		)
	}
	return actual, nil
}

func containersConfig(crunPath string) string {
	return fmt.Sprintf("[engine]\nruntime = \"crun\"\n\n[engine.runtimes]\ncrun = [%q]\n", crunPath)
}

func installPrivilegedFile(ctx context.Context, deps Dependencies, target, contents string) error {
	tmp, err := os.CreateTemp("", "devsy-podman-config-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.WriteString(contents); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := runLinux(
		ctx,
		deps,
		CommandSpec{
			Path:    commandSudo,
			Args:    []string{"install", "-d", "-m", "0755", filepath.Dir(target)},
			Timeout: 15 * time.Second,
		},
	); err != nil {
		return err
	}
	return runLinux(
		ctx,
		deps,
		CommandSpec{
			Path:    commandSudo,
			Args:    []string{"install", "-m", "0644", name, target},
			Timeout: 15 * time.Second,
		},
	)
}

func runLinux(ctx context.Context, deps Dependencies, spec CommandSpec) error {
	result := runLinuxCommand(ctx, spec, deps, 0)
	if !result.Success() {
		return resultError(spec, result)
	}
	if result.Output() != "" {
		deps.Log.Printf(
			"%s %s: %s",
			spec.Path,
			strings.Join(spec.Args, " "),
			strings.TrimSpace(result.Output()),
		)
	}
	return nil
}

func runLinuxCommand(
	ctx context.Context,
	spec CommandSpec,
	deps Dependencies,
	reserve time.Duration,
) CommandResult {
	if deps.Budget != nil {
		spec.Timeout = deps.Budget.Bound(spec.Timeout, reserve)
	}
	return deps.Runner.Run(ctx, spec)
}

func minLinuxDuration(values ...time.Duration) time.Duration {
	result := values[0]
	for _, value := range values[1:] {
		if value < result {
			result = value
		}
	}
	return nonNegativeDuration(result)
}

func resultError(spec CommandSpec, result CommandResult) error {
	return fmt.Errorf(
		"command failed: %s %s exit_code=%s timed_out=%t output=%s",
		spec.Path,
		strings.Join(spec.Args, " "),
		exitCodeString(result.ExitCode),
		result.TimedOut,
		strings.TrimSpace(result.Output()),
	)
}

func configureAppArmor(ctx context.Context, deps Dependencies) error {
	path := "/etc/apparmor.d/podman"
	files := deps.Files
	if files == nil {
		files = osFileSystem{}
	}
	content, err := files.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		result := runLinuxCommand(
			ctx,
			CommandSpec{
				Path:    commandSudo,
				Args:    []string{commandCat, path},
				Timeout: 10 * time.Second,
			},
			deps,
			0,
		)
		if !result.Success() {
			return fmt.Errorf(
				"read AppArmor profile: %w",
				resultError(
					CommandSpec{Path: commandSudo, Args: []string{commandCat, path}},
					result,
				),
			)
		}
		content = []byte(result.Stdout)
	}
	updated, changed := patchAppArmor(content)
	if changed {
		if err := installPrivilegedFile(ctx, deps, path, string(updated)); err != nil {
			return err
		}
	}
	return runLinux(
		ctx,
		deps,
		CommandSpec{
			Path:    commandSudo,
			Args:    []string{"apparmor_parser", "-r", path},
			Timeout: 20 * time.Second,
		},
	)
}

func patchAppArmor(content []byte) ([]byte, bool) {
	old := []byte("profile podman /usr/bin/podman ")
	newValue := []byte("profile podman /usr/{bin,local/bin}/podman ")
	updated := strings.Replace(string(content), string(old), string(newValue), 1)
	return []byte(updated), updated != string(content)
}
