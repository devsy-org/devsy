//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

func runtimeGOOS() string               { return goosWindows }
func platformDefaultPodmanPath() string { return `C:\Program Files\RedHat\Podman\podman.exe` }

const (
	commandWSL                 = "wsl.exe"
	commandMachine             = "machine"
	machineInit                = "init"
	machineInspect             = "inspect"
	machineStart               = "start"
	windowsInitTimeout         = 150 * time.Second
	windowsStartTimeout        = 90 * time.Second
	windowsReadinessTimeout    = 45 * time.Second
	windowsProbeTimeout        = 7 * time.Second
	windowsDiagnosticReserve   = 25 * time.Second
	windowsMinimumRecoveryTime = 60 * time.Second
	windowsHardDeadlinePadding = 10 * time.Second
)

func platformSetup(ctx context.Context, deps Dependencies, cfg SetupConfig) error {
	watchdog := armWatchdog(cfg.BootstrapTimeout+windowsHardDeadlinePadding, deps.Log)
	defer watchdog.Stop()
	return setupWindows(ctx, deps, cfg)
}

func setupWindows(ctx context.Context, deps Dependencies, cfg SetupConfig) error {
	budget, err := NewBudget(cfg.BootstrapTimeout, deps.Clock)
	if err != nil {
		return err
	}
	state := &windowsSetup{ctx: ctx, deps: deps, budget: budget, cfg: cfg}
	return state.run()
}

type windowsSetup struct {
	ctx    context.Context
	deps   Dependencies
	budget *Budget
	cfg    SetupConfig
}

func (s *windowsSetup) run() error {
	wslArgs := []string{"--set-default-version", "2"}
	wsl := s.runCommand(
		CommandSpec{Path: commandWSL, Args: wslArgs, Timeout: 15 * time.Second},
		windowsDiagnosticReserve,
	)
	if !wsl.Success() {
		s.writeDiagnostics(0)
		return fmt.Errorf(
			"PODMAN_WINDOWS_RECOVERY_FAILED: WSL2 setup failed: %s",
			describeCommand(commandWSL, wslArgs, wsl),
		)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		failure := s.startMachineAttempt(attempt)
		if failure == nil {
			s.deps.Log.Printf("[podman-windows] runtime ready attempt=%d", attempt)
			return nil
		}
		if err := s.handleAttemptFailure(attempt, failure); err != nil {
			return err
		}
	}
	return fmt.Errorf("PODMAN_WINDOWS_RECOVERY_FAILED: machine retry limit reached")
}

func (s *windowsSetup) handleAttemptFailure(attempt int, failure error) error {
	s.deps.Log.Printf("[podman-windows] %v", failure)
	canRecover := attempt == 1 &&
		s.budget.Remaining(windowsDiagnosticReserve) >= windowsMinimumRecoveryTime
	reserve := time.Duration(0)
	if canRecover {
		reserve = windowsDiagnosticReserve
	}
	s.writeDiagnostics(reserve)
	if !canRecover || s.budget.Remaining(windowsDiagnosticReserve) < windowsMinimumRecoveryTime {
		if canRecover {
			s.writeDiagnostics(0)
		}
		return fmt.Errorf("PODMAN_WINDOWS_RECOVERY_FAILED: %w", failure)
	}
	if err := s.recover(failure); err != nil {
		return err
	}
	if s.budget.Remaining(windowsDiagnosticReserve) < windowsMinimumRecoveryTime {
		s.writeDiagnostics(0)
		return fmt.Errorf("PODMAN_WINDOWS_RECOVERY_FAILED: %w", failure)
	}
	return nil
}

func (s *windowsSetup) startMachineAttempt(attempt int) error {
	if err := s.initializeMachine(attempt); err != nil {
		return err
	}
	if err := s.configureRootful(attempt); err != nil {
		return err
	}
	return s.startAndWait(attempt)
}

func (s *windowsSetup) initializeMachine(attempt int) error {
	reserve := windowsDiagnosticReserve
	if attempt == 1 {
		reserve += windowsMinimumRecoveryTime
	}
	initTimeout := s.budget.Bound(windowsInitTimeout, reserve)
	if initTimeout <= 0 {
		return fmt.Errorf(
			"PODMAN_WINDOWS_MACHINE_INIT_TIMEOUT attempt=%d output=%s",
			attempt,
			"PODMAN_WINDOWS_BOOTSTRAP_BUDGET_EXHAUSTED: insufficient initialization budget",
		)
	}
	args := []string{commandMachine, machineInit}
	result := s.runCommand(
		CommandSpec{Path: s.cfg.PodmanPath, Args: args, Timeout: initTimeout},
		reserve,
	)
	if result.TimedOut {
		return fmt.Errorf(
			"PODMAN_WINDOWS_MACHINE_INIT_TIMEOUT attempt=%d %s",
			attempt,
			describeCommand(s.cfg.PodmanPath, args, result),
		)
	}
	if result.Success() {
		return nil
	}
	if strings.Contains(result.Output(), "already exists") {
		return s.verifyExistingMachine(attempt)
	}
	return fmt.Errorf(
		"PODMAN_WINDOWS_MACHINE_START_FAILED attempt=%d %s",
		attempt,
		describeCommand(s.cfg.PodmanPath, args, result),
	)
}

func (s *windowsSetup) verifyExistingMachine(attempt int) error {
	args := []string{commandMachine, machineInspect}
	result := s.runCommand(
		CommandSpec{Path: s.cfg.PodmanPath, Args: args, Timeout: 5 * time.Second},
		windowsDiagnosticReserve,
	)
	if !result.Success() || strings.TrimSpace(result.Output()) == "" {
		return fmt.Errorf(
			"PODMAN_WINDOWS_MACHINE_START_FAILED attempt=%d machine exists without a usable record: %s",
			attempt,
			describeCommand(s.cfg.PodmanPath, args, result),
		)
	}
	return nil
}

func (s *windowsSetup) configureRootful(attempt int) error {
	args := []string{commandMachine, "set", "--rootful"}
	result := s.runCommand(
		CommandSpec{Path: s.cfg.PodmanPath, Args: args, Timeout: 20 * time.Second},
		windowsDiagnosticReserve,
	)
	if !result.Success() {
		return fmt.Errorf(
			"PODMAN_WINDOWS_MACHINE_START_FAILED attempt=%d %s",
			attempt,
			describeCommand(s.cfg.PodmanPath, args, result),
		)
	}
	return nil
}

func (s *windowsSetup) startAndWait(attempt int) error {
	args := []string{commandMachine, machineStart}
	result := s.runCommand(
		CommandSpec{Path: s.cfg.PodmanPath, Args: args, Timeout: windowsStartTimeout},
		windowsDiagnosticReserve,
	)
	if result.TimedOut {
		return fmt.Errorf(
			"PODMAN_WINDOWS_MACHINE_START_TIMEOUT attempt=%d %s",
			attempt,
			describeCommand(s.cfg.PodmanPath, args, result),
		)
	}
	if !result.Success() {
		return fmt.Errorf(
			"PODMAN_WINDOWS_MACHINE_START_FAILED attempt=%d %s",
			attempt,
			describeCommand(s.cfg.PodmanPath, args, result),
		)
	}
	return s.waitForReady(attempt)
}

func (s *windowsSetup) waitForReady(attempt int) error {
	readinessStarted := s.deps.Clock.Now()
	var last CommandResult
	for {
		readinessLeft := windowsReadinessTimeout - s.deps.Clock.Now().Sub(readinessStarted)
		budgetLeft := s.budget.Remaining(windowsDiagnosticReserve)
		if readinessLeft <= 0 || budgetLeft <= 0 {
			break
		}
		probe := minDuration(windowsProbeTimeout, readinessLeft, budgetLeft)
		last = s.runCommand(
			CommandSpec{Path: s.cfg.PodmanPath, Args: []string{"info"}, Timeout: probe},
			windowsDiagnosticReserve,
		)
		if last.Success() {
			return nil
		}
		sleep := minDuration(
			2*time.Second,
			windowsReadinessTimeout-s.deps.Clock.Now().Sub(readinessStarted),
			s.budget.Remaining(windowsDiagnosticReserve),
		)
		s.deps.Clock.Sleep(sleep)
	}
	return fmt.Errorf(
		"PODMAN_WINDOWS_READINESS_TIMEOUT attempt=%d last=%s",
		attempt,
		describeCommand(s.cfg.PodmanPath, []string{"info"}, last),
	)
}

func (s *windowsSetup) recover(failure error) error {
	s.deps.Log.Printf("[podman-windows] recovery start")
	cleanupFailed := false
	var cleanupErr error
	for _, command := range []CommandSpec{
		{Path: s.cfg.PodmanPath, Args: []string{commandMachine, "stop"}, Timeout: 10 * time.Second},
		{Path: s.cfg.PodmanPath, Args: []string{commandMachine, "rm", "-f"}, Timeout: 10 * time.Second},
		{Path: commandWSL, Args: []string{"--shutdown"}, Timeout: 10 * time.Second},
	} {
		result := s.runCommand(command, windowsDiagnosticReserve)
		machineAbsent := command.Path == s.cfg.PodmanPath && isDefaultMachineAbsent(result)
		if !result.Success() && !machineAbsent {
			cleanupFailed = true
			cleanupErr = fmt.Errorf("%s", describeCommand(command.Path, command.Args, result))
		}
	}
	if err := s.removeStaleWSLDistribution(); err != nil {
		cleanupFailed = true
		cleanupErr = err
	}
	if cleanupFailed {
		s.writeDiagnostics(0)
		return fmt.Errorf(
			"PODMAN_WINDOWS_RECOVERY_FAILED: recovery cleanup command failed after %v: %w",
			failure,
			cleanupErr,
		)
	}
	return nil
}

func (s *windowsSetup) removeStaleWSLDistribution() error {
	listArgs := []string{"--list", "--quiet"}
	listed := s.runCommand(
		CommandSpec{Path: commandWSL, Args: listArgs, Timeout: 5 * time.Second},
		windowsDiagnosticReserve,
	)
	if !listed.Success() {
		return fmt.Errorf("%s", describeCommand(commandWSL, listArgs, listed))
	}
	if !containsExact(parseWSLDistributions([]byte(listed.Output())), "podman-machine-default") {
		return nil
	}
	args := []string{"--unregister", "podman-machine-default"}
	removed := s.runCommand(
		CommandSpec{Path: commandWSL, Args: args, Timeout: 15 * time.Second},
		windowsDiagnosticReserve,
	)
	if !removed.Success() {
		return fmt.Errorf("%s", describeCommand(commandWSL, args, removed))
	}
	return nil
}

func (s *windowsSetup) runCommand(spec CommandSpec, reserve time.Duration) CommandResult {
	timeout := s.budget.Bound(spec.Timeout, reserve)
	spec.Timeout = timeout
	spec.Reserve = reserve
	started := s.deps.Clock.Now()
	s.deps.Log.Printf(
		"[podman-windows] command_start path=%s args='%s' timeout_ms=%d remaining_ms=%d",
		spec.Path,
		strings.Join(spec.Args, " "),
		timeout.Milliseconds(),
		s.budget.Remaining(reserve).Milliseconds(),
	)
	result := s.deps.Runner.Run(s.ctx, spec)
	remaining := s.budget.Remaining(reserve)
	if remaining <= 0 {
		result.TimedOut = true
		result.ExitCode = nil
		result.Stderr = "PODMAN_WINDOWS_BOOTSTRAP_BUDGET_EXHAUSTED: " + result.Output()
	}
	s.deps.Log.Printf(
		"[podman-windows] command_end path=%s args='%s' elapsed_ms=%d exit_code=%s timed_out=%t remaining_ms=%d",
		spec.Path,
		strings.Join(spec.Args, " "),
		s.deps.Clock.Now().Sub(started).Milliseconds(),
		exitCodeString(result.ExitCode),
		result.TimedOut,
		remaining.Milliseconds(),
	)
	return result
}

func (s *windowsSetup) writeDiagnostics(reserve time.Duration) {
	commands := []CommandSpec{
		{Path: s.cfg.PodmanPath, Args: []string{"version"}, Timeout: 3 * time.Second},
		{Path: s.cfg.PodmanPath, Args: []string{commandMachine, "list"}, Timeout: 3 * time.Second},
		{
			Path:    s.cfg.PodmanPath,
			Args:    []string{"machine", machineInspect},
			Timeout: 3 * time.Second,
		},
		{
			Path:    s.cfg.PodmanPath,
			Args:    []string{"system", "connection", "list"},
			Timeout: 3 * time.Second,
		},
		{Path: commandWSL, Args: []string{"--status"}, Timeout: 3 * time.Second},
		{Path: commandWSL, Args: []string{"-l", "-v"}, Timeout: 3 * time.Second},
	}
	for _, command := range commands {
		if s.budget.Remaining(reserve) <= 0 {
			break
		}
		result := s.runCommand(command, reserve)
		s.deps.Log.Printf(
			"[podman-windows] %s %s: exit=%s timeout=%t %s",
			command.Path,
			strings.Join(command.Args, " "),
			exitCodeString(result.ExitCode),
			result.TimedOut,
			result.Output(),
		)
	}
	for _, process := range matchingWindowsProcesses() {
		s.deps.Log.Printf("[podman-windows] process pid=%d name=%s", process.PID, process.Name)
	}
}

func describeCommand(path string, args []string, result CommandResult) string {
	return fmt.Sprintf(
		"executable=%q args=%q exit_code=%s timed_out=%t output=%s",
		path,
		strings.Join(args, " "),
		exitCodeString(result.ExitCode),
		result.TimedOut,
		result.Output(),
	)
}

func isDefaultMachineAbsent(result CommandResult) bool {
	if result.TimedOut || result.ExitCode == nil || *result.ExitCode == 0 {
		return false
	}
	output := strings.TrimSpace(result.Output())
	return output == "podman-machine-default: VM does not exist" ||
		output == "Error: podman-machine-default: VM does not exist"
}

func minDuration(values ...time.Duration) time.Duration {
	result := values[0]
	for _, value := range values[1:] {
		if value < result {
			result = value
		}
	}
	if result < 0 {
		return 0
	}
	return result
}

type (
	watchdog      interface{ Stop() }
	timerWatchdog struct {
		stop chan struct{}
		once sync.Once
	}
)

func armWatchdog(timeout time.Duration, log *Logger) watchdog {
	log.Printf(
		"[podman-windows] hard_deadline_armed pid=%d timeout_ms=%d timeout_exit_code=124",
		windows.GetCurrentProcessId(),
		timeout.Milliseconds(),
	)
	watch := &timerWatchdog{stop: make(chan struct{})}
	go func() {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-timer.C:
			fmt.Fprintln(os.Stderr, "PODMAN_WINDOWS_BOOTSTRAP_HARD_TIMEOUT")
			if err := windows.TerminateProcess(windows.CurrentProcess(), 124); err != nil {
				fmt.Fprintf(
					os.Stderr,
					"PODMAN_WINDOWS_BOOTSTRAP_HARD_TIMEOUT: native termination failed: %v\n",
					err,
				)
			}
			os.Exit(124)
		case <-watch.stop:
		}
	}()
	return watch
}

func (w *timerWatchdog) Stop() { w.once.Do(func() { close(w.stop) }) }
