//go:build windows

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	testMachineInit  = "machine init"
	testMachineStart = "machine start"
	testWSLList      = "--list --quiet"
)

type windowsFakeRunner struct {
	calls          []recordedWindowsCommand
	clock          *fakeClock
	results        map[string][]CommandResult
	durations      map[string]time.Duration
	durationQueues map[string][]time.Duration
}
type recordedWindowsCommand struct {
	path    string
	args    []string
	timeout time.Duration
}

func successResult(out string) CommandResult {
	code := 0
	return CommandResult{ExitCode: &code, Stdout: out}
}

func failureResult(out string) CommandResult {
	code := 1
	return CommandResult{ExitCode: &code, Stderr: out}
}

func (r *windowsFakeRunner) Run(_ context.Context, spec CommandSpec) CommandResult {
	command := strings.Join(spec.Args, " ")
	r.calls = append(
		r.calls,
		recordedWindowsCommand{
			path:    spec.Path,
			args:    append([]string(nil), spec.Args...),
			timeout: spec.Timeout,
		},
	)
	duration := time.Duration(0)
	if queue := r.durationQueues[command]; len(queue) > 0 {
		duration = queue[0]
		r.durationQueues[command] = queue[1:]
	} else {
		duration = r.durations[command]
	}
	if duration > 0 {
		used := min(duration, spec.Timeout)
		r.clock.Sleep(used)
		if duration > spec.Timeout {
			return CommandResult{TimedOut: true, Stderr: "timed out"}
		}
	}
	if queue := r.results[command]; len(queue) > 0 {
		out := queue[0]
		r.results[command] = queue[1:]
		return out
	}
	return successResult("")
}

func hasWindowsCall(r *windowsFakeRunner, command string) bool {
	for _, c := range r.calls {
		if strings.Join(c.args, " ") == command {
			return true
		}
	}
	return false
}

func countWindowsCalls(r *windowsFakeRunner, command string) int {
	count := 0
	for _, c := range r.calls {
		if strings.Join(c.args, " ") == command {
			count++
		}
	}
	return count
}

func windowsDependencies(r *windowsFakeRunner, clock *fakeClock) Dependencies {
	return Dependencies{Runner: r, Clock: clock, Log: &Logger{Out: io.Discard, Err: io.Discard}}
}

func windowsConfig(t *testing.T) SetupConfig {
	t.Helper()
	podman := filepath.Join(t.TempDir(), "podman.exe")
	if err := os.WriteFile(podman, []byte(""), 0o700); err != nil {
		t.Fatal(err)
	}
	return SetupConfig{Mode: ModeRootful, PodmanPath: podman, BootstrapTimeout: 270 * time.Second}
}

func TestWindowsHealthyBootstrapAndExistingMachine(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1, 0)}
	runner := &windowsFakeRunner{clock: clock, results: map[string][]CommandResult{
		testMachineInit:   {failureResult("machine already exists")},
		"machine inspect": {successResult(`{"Name":"podman-machine-default"}`)},
	}, durations: map[string]time.Duration{}}
	if err := setupWindows(
		context.Background(),
		windowsDependencies(runner, clock),
		windowsConfig(t),
	); err != nil {
		t.Fatal(err)
	}
	if countWindowsCalls(runner, "machine start") != 1 ||
		countWindowsCalls(runner, "machine inspect") != 1 {
		t.Fatalf("existing-machine flow calls: %+v", runner.calls)
	}
	if countWindowsCalls(runner, "version") != 0 {
		t.Fatal("healthy bootstrap emitted diagnostics")
	}
}

func TestWindowsFailedStartRecoversOnlyExactWSLDistro(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1, 0)}
	runner := &windowsFakeRunner{clock: clock, results: map[string][]CommandResult{
		testMachineStart: {failureResult("pipe busy"), successResult("")},
		testWSLList: {
			successResult("Ubuntu\npodman-machine-default\npodman-machine-default-other\n"),
		},
	}, durations: map[string]time.Duration{}}
	if err := setupWindows(
		context.Background(),
		windowsDependencies(runner, clock),
		windowsConfig(t),
	); err != nil {
		t.Fatal(err)
	}
	if countWindowsCalls(runner, testMachineStart) != 2 ||
		!hasWindowsCall(runner, "machine rm -f") {
		t.Fatalf("recovery did not retry: %+v", runner.calls)
	}
	if !hasWindowsCall(runner, "--unregister podman-machine-default") ||
		countWindowsCalls(runner, "--unregister podman-machine-default-other") != 0 {
		t.Fatalf("WSL cleanup was not exact: %+v", runner.calls)
	}
}

func TestWindowsFirstInitReservesDiagnosticsAndRecovery(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1, 0).Add(100001 * time.Millisecond)}
	runner := &windowsFakeRunner{
		clock:     clock,
		results:   map[string][]CommandResult{},
		durations: map[string]time.Duration{testMachineInit: 100 * time.Second},
	}
	config := windowsConfig(t)
	budget, err := NewBudget(270*time.Second, clock)
	if err != nil {
		t.Fatal(err)
	}
	// Shift the budget origin back to model work already consumed before init.
	budget.started = clock.Now().Add(-100001 * time.Millisecond)
	state := &windowsSetup{
		ctx:    context.Background(),
		deps:   windowsDependencies(runner, clock),
		budget: budget,
		cfg:    config,
	}
	if err := state.startMachineAttempt(1); err == nil {
		t.Fatal("expected init timeout")
	}
	if len(runner.calls) != 1 || runner.calls[0].timeout != 84999*time.Millisecond {
		t.Fatalf("init bound = %+v", runner.calls)
	}
}

func TestWindowsPartialInitRecoveryRetriesWithFreshBudget(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1, 0)}
	runner := &windowsFakeRunner{
		clock: clock, results: map[string][]CommandResult{
			testMachineInit: {
				successResult(""),
			},
			"machine stop":  {failureResult("Error: podman-machine-default: VM does not exist")},
			"machine rm -f": {failureResult("Error: podman-machine-default: VM does not exist")},
			testWSLList: {
				successResult("Ubuntu\npodman-machine-default\npodman-machine-default-other"),
			},
		},
		durations:      map[string]time.Duration{},
		durationQueues: map[string][]time.Duration{testMachineInit: {151 * time.Second}},
	}
	if err := setupWindows(
		context.Background(),
		windowsDependencies(runner, clock),
		windowsConfig(t),
	); err != nil {
		t.Fatal(err)
	}
	if countWindowsCalls(runner, testMachineInit) != 2 ||
		!hasWindowsCall(runner, "--unregister podman-machine-default") {
		t.Fatalf("partial-init recovery failed: %+v", runner.calls)
	}
	var secondInit time.Duration
	seenInit := false
	for _, call := range runner.calls {
		if strings.Join(call.args, " ") == testMachineInit {
			if seenInit {
				secondInit = call.timeout
				break
			}
			seenInit = true
		}
	}
	if secondInit != 95*time.Second {
		t.Fatalf("second init timeout = %s, want 95s", secondInit)
	}
}

func TestWindowsUnexpectedCleanupFailurePreventsRetry(t *testing.T) {
	failedCommands := []string{
		"machine stop", "machine rm -f", "--shutdown", testWSLList,
		"--unregister podman-machine-default",
	}
	for _, failedCommand := range failedCommands {
		t.Run(failedCommand, func(t *testing.T) {
			clock := &fakeClock{now: time.Unix(1, 0)}
			results := map[string][]CommandResult{
				testMachineInit: {CommandResult{TimedOut: true, Stderr: "init timed out"}},
				testWSLList:     {successResult("podman-machine-default")},
			}
			results[failedCommand] = []CommandResult{failureResult("cleanup failed")}
			runner := &windowsFakeRunner{
				clock:     clock,
				results:   results,
				durations: map[string]time.Duration{},
			}
			err := setupWindows(
				context.Background(),
				windowsDependencies(runner, clock),
				windowsConfig(t),
			)
			if err == nil ||
				!strings.Contains(
					err.Error(),
					"PODMAN_WINDOWS_RECOVERY_FAILED: recovery cleanup command failed",
				) {
				t.Fatalf("cleanup error = %v", err)
			}
			if countWindowsCalls(runner, testMachineInit) != 1 {
				t.Fatalf("retry started after %s failed: %+v", failedCommand, runner.calls)
			}
		})
	}
}

func TestWindowsSecondFailedAttemptReturnsStableRecoveryMarker(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1, 0)}
	runner := &windowsFakeRunner{
		clock: clock,
		results: map[string][]CommandResult{
			testMachineStart: {failureResult("pipe busy"), failureResult("pipe busy")},
		},
		durations: map[string]time.Duration{},
	}
	err := setupWindows(context.Background(), windowsDependencies(runner, clock), windowsConfig(t))
	if err == nil || !strings.HasPrefix(err.Error(), "PODMAN_WINDOWS_RECOVERY_FAILED:") ||
		!strings.Contains(err.Error(), "pipe busy") {
		t.Fatalf("terminal error = %v", err)
	}
	if countWindowsCalls(runner, "version") != 2 {
		t.Fatalf("terminal diagnostics count = %d", countWindowsCalls(runner, "version"))
	}
}

func TestWindowsBudgetExhaustionRejectsLateSuccess(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1, 0)}
	runner := &windowsFakeRunner{
		clock:     clock,
		results:   map[string][]CommandResult{},
		durations: map[string]time.Duration{"--set-default-version 2": time.Second},
	}
	budget, err := NewBudget(500*time.Millisecond, clock)
	if err != nil {
		t.Fatal(err)
	}
	state := &windowsSetup{
		ctx:    context.Background(),
		deps:   windowsDependencies(runner, clock),
		budget: budget,
		cfg:    windowsConfig(t),
	}
	result := state.runCommand(
		CommandSpec{
			Path:    commandWSL,
			Args:    []string{"--set-default-version", "2"},
			Timeout: 5 * time.Second,
		},
		0,
	)
	if !result.TimedOut || result.ExitCode != nil ||
		!strings.Contains(result.Output(), "PODMAN_WINDOWS_BOOTSTRAP_BUDGET_EXHAUSTED") {
		t.Fatalf("late success accepted: %+v", result)
	}
}

func TestWindowsWatchdogTerminatesAndCanBeDisarmed(t *testing.T) {
	if mode := os.Getenv("DEVSY_PODMAN_WATCHDOG_CHILD"); mode != "" {
		runWatchdogChild(mode)
		return
	}
	verifyWatchdogExpires(t)
	verifyWatchdogCanBeDisarmed(t)
}

func runWatchdogChild(mode string) {
	wd := armWatchdog(750*time.Millisecond, &Logger{Out: os.Stdout, Err: os.Stderr})
	if mode == "complete" {
		wd.Stop()
		wd.Stop()
		time.Sleep(1500 * time.Millisecond)
		fmtPrint("watchdog-disarmed\n")
		os.Exit(0)
	}
	time.Sleep(30 * time.Second)
	os.Exit(0)
}

func verifyWatchdogExpires(t *testing.T) {
	t.Helper()
	output, err := runWatchdogChildProcess(t, "blocked")
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 124 {
		t.Fatalf("blocked watchdog exit=%v output=%s", err, output)
	}
	if !strings.Contains(string(output), "hard_deadline_armed") ||
		!strings.Contains(string(output), "PODMAN_WINDOWS_BOOTSTRAP_HARD_TIMEOUT") {
		t.Fatalf("watchdog markers missing: %s", output)
	}
}

func verifyWatchdogCanBeDisarmed(t *testing.T) {
	t.Helper()
	output, err := runWatchdogChildProcess(t, "complete")
	if err != nil || !strings.Contains(string(output), "watchdog-disarmed") {
		t.Fatalf("disarmed watchdog result err=%v output=%s", err, output)
	}
}

func runWatchdogChildProcess(t *testing.T, mode string) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(
		ctx,
		os.Args[0],
		"-test.run=^TestWindowsWatchdogTerminatesAndCanBeDisarmed$",
	)
	cmd.Env = append(os.Environ(), "DEVSY_PODMAN_WATCHDOG_CHILD="+mode)
	return cmd.CombinedOutput()
}

func fmtPrint(s string) { _, _ = fmt.Fprint(os.Stdout, s) }
