package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

const (
	defaultArtifactName      = "provider-update-windows"
	windowsTestTimeout       = "10m"
	providerUpdateRunPattern = "^(TestProviderUpdate|TestStatUpdatePath|" +
		"TestExtractConfinedUpdateTar|TestGNUArchiveFixture)"
)

var providerUpdatePackages = []string{
	"github.com/devsy-org/devsy/pkg/workspace",
	"github.com/devsy-org/devsy/pkg/provider",
}

func main() {
	os.Exit(runCLI(os.Args[1:]))
}

func runCLI(args []string) int {
	flags := flag.NewFlagSet("provider-update-ci", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	artifactsDir := os.Getenv("RUNNER_TEMP")
	flags.StringVar(
		&artifactsDir, "artifacts-dir", artifactsDir, "directory for test output artifacts",
	)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		reportf(os.Stderr, "unexpected positional arguments\n")
		return 2
	}
	if artifactsDir == "" {
		reportf(os.Stderr, "--artifacts-dir or RUNNER_TEMP is required\n")
		return 2
	}
	return runProviderUpdateTests(artifactsDir, os.Stdout, os.Stderr)
}

func runProviderUpdateTests(artifactsDir string, stdout, stderr io.Writer) int {
	logFile, capture, err := openTestArtifacts(artifactsDir, stdout, stderr)
	if err != nil {
		reportf(stderr, "%v\n", err)
		return 1
	}

	expected, discoveryErr := discoverProviderTests(logFile, stdout, stderr)
	result := testRunResult{discoveryErr: discoveryErr}
	if discoveryErr == nil {
		result, capture = runGoTestsWithRetry(logFile, capture, stdout, stderr)
	} else {
		capture.finish()
		result.exitCode = executionExitCode(result)
	}
	result.captureErr = capture.err()
	exitWriteErr, logCloseErr := finishTestArtifacts(artifactsDir, logFile, result.exitCode)
	result.exitWriteErr = exitWriteErr
	result.logCloseErr = logCloseErr
	reportExecutionErrors(stderr, result)
	if result.exitCode != 0 {
		return result.exitCode
	}
	return validateResult(result, expected, capture.events(), stderr)
}

type testRunResult struct {
	discoveryErr error
	startErr     error
	waitErr      error
	exitCode     int
	exitWriteErr error
	logCloseErr  error
	captureErr   error
}

func openTestArtifacts(
	artifactsDir string,
	stdout, stderr io.Writer,
) (*os.File, *outputCapture, error) {
	if err := os.MkdirAll(artifactsDir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("create artifact directory: %w", err)
	}
	logPath := filepath.Join(artifactsDir, defaultArtifactName+".jsonl")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("create Go test output artifact: %w", err)
	}
	return logFile, newOutputCapture(logFile, stdout, stderr), nil
}

func runGoTests(capture *outputCapture) testRunResult {
	command := providerTestCommand("-run")
	command.Stdout = capture.stdoutWriter()
	command.Stderr = capture.stderrWriter()
	startErr := command.Start()
	if startErr != nil {
		return testRunResult{startErr: startErr}
	}
	return testRunResult{waitErr: command.Wait()}
}

func finishTestArtifacts(artifactsDir string, logFile *os.File, goExitCode int) (error, error) {
	exitPath := filepath.Join(artifactsDir, defaultArtifactName+".exitcode")
	exitWriteErr := os.WriteFile(exitPath, []byte(strconv.Itoa(goExitCode)), 0o644)
	logCloseErr := logFile.Close()
	return exitWriteErr, logCloseErr
}

func reportExecutionErrors(stderr io.Writer, result testRunResult) {
	if result.discoveryErr != nil {
		reportf(stderr, "::error::%v\n", result.discoveryErr)
	}
	reportStartError(stderr, result.startErr)
	reportWaitError(stderr, result)
	reportArtifactErrors(stderr, result)
	reportCaptureError(stderr, result.captureErr)
}

func reportStartError(stderr io.Writer, err error) {
	if err != nil {
		reportf(stderr, "start Go provider update tests: %v\n", err)
	}
}

func reportWaitError(stderr io.Writer, result testRunResult) {
	if result.exitCode != 0 && result.startErr == nil && result.discoveryErr == nil {
		reportf(
			stderr,
			"::error::Provider update tests failed with exit code %d\n",
			result.exitCode,
		)
	}
	if result.waitErr == nil || result.exitCode != 1 {
		return
	}
	if _, ok := errors.AsType[*exec.ExitError](result.waitErr); !ok {
		reportf(stderr, "wait for Go provider update tests: %v\n", result.waitErr)
	}
}

func reportArtifactErrors(stderr io.Writer, result testRunResult) {
	if result.exitWriteErr != nil {
		reportf(stderr, "write Go test exit-code artifact: %v\n", result.exitWriteErr)
	}
	if result.logCloseErr != nil {
		reportf(stderr, "close Go test output artifact: %v\n", result.logCloseErr)
	}
}

func reportCaptureError(stderr io.Writer, err error) {
	if err != nil {
		reportf(stderr, "capture Go test output: %v\n", err)
	}
}

func reportf(stderr io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(stderr, format, args...)
}

func validateResult(
	result testRunResult,
	expected map[testID]struct{},
	events map[testID]testState,
	stderr io.Writer,
) int {
	issues := validateTestCoverage(expected, events)
	for _, issue := range issues {
		reportf(stderr, "::error::%s\n", issue)
	}
	if len(issues) != 0 ||
		result.exitWriteErr != nil ||
		result.logCloseErr != nil ||
		result.captureErr != nil {
		return 1
	}
	return 0
}

func processExitCode(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		if code := exitErr.ExitCode(); code >= 0 {
			return code
		}
	}
	return 1
}

func executionExitCode(result testRunResult) int {
	if result.discoveryErr != nil {
		return processExitCode(result.discoveryErr)
	}
	if result.startErr != nil {
		return 1
	}
	return processExitCode(result.waitErr)
}
