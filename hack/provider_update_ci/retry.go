package main

import (
	"io"
	"strings"
)

const (
	maxTestAttempts       = 2
	windowsAccessViolated = 0xc0000005
)

// Prefixes the Go runtime prints when the test process itself dies. The raw
// pointer panic value is the heap-corruption symptom seen in
// golang/go#81238; test code never panics with one.
var runtimeFatalPrefixes = []string{
	"fatal error:",
	"Exception 0xc0000005",
	"panic: (interface {}) 0x",
}

// runGoTestsWithRetry reruns the tests once when the Go runtime itself crashed.
// Windows/amd64 AMX runners corrupt the heap when an exception frame overflows
// the goroutine stack reserve (golang/go#81238, unfixed). Remove this retry once
// a released Go toolchain contains the upstream fix.
func runGoTestsWithRetry(
	logFile io.Writer,
	capture *outputCapture,
	stdout, stderr io.Writer,
) (testRunResult, *outputCapture) {
	for attempt := 1; ; attempt++ {
		result := runGoTests(capture)
		capture.finish()
		result.exitCode = executionExitCode(result)
		if attempt == maxTestAttempts || !isRuntimeCrash(result, capture) {
			return result, capture
		}
		reportf(
			stderr,
			"::warning::Go runtime crashed on Windows (golang/go#81238); "+
				"retrying provider update tests (attempt %d of %d)\n",
			attempt+1,
			maxTestAttempts,
		)
		capture = newOutputCapture(logFile, stdout, stderr)
	}
}

func isRuntimeCrash(result testRunResult, capture *outputCapture) bool {
	if result.exitCode == 0 || result.startErr != nil || capture.err() != nil {
		return false
	}
	for _, state := range capture.events() {
		if state.failed {
			return false
		}
	}
	return uint32(result.exitCode) == windowsAccessViolated || capture.sawRuntimeFatal()
}

func isRuntimeFatalLine(line string) bool {
	for _, prefix := range runtimeFatalPrefixes {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}
