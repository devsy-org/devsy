//go:build windows

package main

import (
	"bytes"
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
	stubStrayProcessSweep(t)
	podman := filepath.Join(t.TempDir(), "podman.exe")
	if err := os.WriteFile(podman, []byte(""), 0o700); err != nil {
		t.Fatal(err)
	}
	return SetupConfig{Mode: ModeRootful, PodmanPath: podman, BootstrapTimeout: 270 * time.Second}
}

// stubStrayProcessSweep replaces the real system-wide process sweep with a
// no-op for the duration of the test: fake-runner tests exercise setup
// failure paths that call terminateStrayProcessesHook, and letting that
// reach the real OS-level kill could terminate unrelated podman/wsl
// processes on a developer's machine.
func stubStrayProcessSweep(t *testing.T) {
	t.Helper()
	original := terminateStrayProcessesHook
	terminateStrayProcessesHook = func(*Logger) {}
	t.Cleanup(func() { terminateStrayProcessesHook = original })
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

func TestWindowsMachineInitRetriesTransientPullError(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1, 0)}
	runner := &windowsFakeRunner{clock: clock, results: map[string][]CommandResult{
		testMachineInit: {
			failureResult("Error: writing blob: happened during read: unexpected EOF"),
			successResult(""),
		},
	}, durations: map[string]time.Duration{}}
	if err := setupWindows(
		context.Background(),
		windowsDependencies(runner, clock),
		windowsConfig(t),
	); err != nil {
		t.Fatal(err)
	}
	if countWindowsCalls(runner, testMachineInit) != 2 {
		t.Fatalf("transient pull error did not retry machine init: %+v", runner.calls)
	}
	if countWindowsCalls(runner, "machine stop") != 0 {
		t.Fatalf("transient pull retry should not trigger full recovery: %+v", runner.calls)
	}
}

func TestWindowsMachineInitTransientPullRetryIsBounded(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1, 0)}
	transientFailure := failureResult("Error: writing blob: happened during read: unexpected EOF")
	runner := &windowsFakeRunner{clock: clock, results: map[string][]CommandResult{
		testMachineInit: {
			transientFailure, transientFailure, transientFailure,
			transientFailure, transientFailure, transientFailure,
		},
	}, durations: map[string]time.Duration{}}
	err := setupWindows(context.Background(), windowsDependencies(runner, clock), windowsConfig(t))
	if err == nil || !strings.Contains(err.Error(), "unexpected EOF") {
		t.Fatalf("expected terminal transient-pull failure, got %v", err)
	}
	if countWindowsCalls(runner, testMachineInit) != (maxTransientPullRetries+1)*2 {
		t.Fatalf("expected bounded retries across both attempts: %+v", runner.calls)
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

func TestWindowsStartHangIsAnnotatedAndRetriedOnce(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1, 0)}
	runner := &windowsFakeRunner{
		clock:   clock,
		results: map[string][]CommandResult{},
		durationQueues: map[string][]time.Duration{
			testMachineStart: {windowsStartTimeout + time.Second},
		},
	}
	var output bytes.Buffer
	files := &summaryFS{}
	deps := windowsDependencies(runner, clock)
	deps.Log = &Logger{Out: &output, Err: io.Discard}
	deps.Files = files
	cfg := windowsConfig(t)
	cfg.GitHubStepSummaryPath = testSummaryPath

	if err := setupWindows(context.Background(), deps, cfg); err != nil {
		t.Fatalf("retry after hang should recover: %v", err)
	}

	wantAnnotation := "::warning title=Windows podman start hang::" +
		"PODMAN_WINDOWS_MACHINE_START_TIMEOUT attempt=1"
	if strings.Count(output.String(), wantAnnotation) != 1 {
		t.Fatalf("annotation missing or repeated: %s", output.String())
	}
	summary := files.appended[testSummaryPath].String()
	if !strings.Contains(summary, "machine list") || !strings.Contains(summary, "wsl.exe -l -v") {
		t.Fatalf("summary lacks diagnostics: %s", summary)
	}
	if countWindowsCalls(runner, testMachineStart) != 2 {
		t.Fatalf("start calls = %d, want 2", countWindowsCalls(runner, testMachineStart))
	}
}

func TestWindowsSecondStartHangFailsStepWithBothAnnotations(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1, 0)}
	hang := windowsStartTimeout + time.Second
	runner := &windowsFakeRunner{
		clock:          clock,
		results:        map[string][]CommandResult{},
		durationQueues: map[string][]time.Duration{testMachineStart: {hang, hang}},
	}
	var output bytes.Buffer
	deps := windowsDependencies(runner, clock)
	deps.Log = &Logger{Out: &output, Err: io.Discard}

	err := setupWindows(context.Background(), deps, windowsConfig(t))

	if err == nil || !strings.HasPrefix(err.Error(), "PODMAN_WINDOWS_RECOVERY_FAILED:") {
		t.Fatalf("second hang must fail the step, got %v", err)
	}
	for _, attempt := range []string{"attempt=1", "attempt=2"} {
		want := "::warning title=Windows podman start hang::" +
			"PODMAN_WINDOWS_MACHINE_START_TIMEOUT " + attempt
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing annotation for %s: %s", attempt, output.String())
		}
	}
}

func TestWindowsNonHangFailureIsNotAnnotatedAsHang(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1, 0)}
	runner := &windowsFakeRunner{
		clock: clock,
		results: map[string][]CommandResult{
			testMachineStart: {failureResult("pipe busy"), failureResult("pipe busy")},
		},
		durations: map[string]time.Duration{},
	}
	var output bytes.Buffer
	deps := windowsDependencies(runner, clock)
	deps.Log = &Logger{Out: &output, Err: io.Discard}

	if err := setupWindows(context.Background(), deps, windowsConfig(t)); err == nil {
		t.Fatal("expected failure")
	}

	if strings.Contains(output.String(), "::warning") {
		t.Fatalf("non-timeout failure annotated as a hang: %s", output.String())
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
	// This process is a fresh re-exec of the test binary, so package vars
	// are back to their defaults; neutralize the real system-wide sweep so
	// the watchdog firing in these tests cannot kill unrelated podman/wsl
	// processes on the host.
	terminateStrayProcessesHook = func(*Logger) {}
	if mode == "blocked-stderr" {
		reader, stderr, err := os.Pipe()
		if err != nil {
			os.Exit(1)
		}
		defer reader.Close()
		os.Stderr = stderr
		writeDone := make(chan struct{})
		go func() {
			defer close(writeDone)
			_, _ = stderr.Write(make([]byte, 1<<20))
		}()
		select {
		case <-writeDone:
			os.Exit(1)
		case <-time.After(100 * time.Millisecond):
		}
		armWatchdog(750*time.Millisecond, &Logger{Out: io.Discard, Err: stderr}, hangReporter{})
		select {}
	}
	wd := armWatchdog(
		750*time.Millisecond,
		&Logger{Out: os.Stdout, Err: os.Stderr},
		hangReporter{},
	)
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
		!strings.Contains(string(output), "hard_deadline_exceeded") ||
		strings.Contains(string(output), "PODMAN_WINDOWS_BOOTSTRAP_HARD_TIMEOUT") {
		t.Fatalf("watchdog markers missing: %s", output)
	}
	output, err = runWatchdogChildProcess(t, "blocked-stderr")
	exit, ok = err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 124 {
		t.Fatalf("blocked-stderr watchdog exit=%v output=%s", err, output)
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
