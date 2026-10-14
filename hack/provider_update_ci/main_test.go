package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const (
	fakeModeEnv    = "DEVSY_PROVIDER_UPDATE_CI_FAKE_MODE"
	cliModeEnv     = "DEVSY_PROVIDER_UPDATE_CI_RUN_CLI"
	fakeExitEnv    = "DEVSY_PROVIDER_UPDATE_CI_FAKE_EXIT"
	largeLineEnv   = "DEVSY_PROVIDER_UPDATE_CI_LARGE_LINE"
	fakeGoBasename = "go"
	missingMode    = "missing"
	skipMode       = "skip"
	noPassMode     = "no-pass"
	multipleMode   = "multiple"
)

var fakeCaseExceptions = map[string]map[int][]string{
	missingMode: {0: nil, 1: nil},
	skipMode:    {0: {"skip"}},
	noPassMode:  {0: {"run"}},
	multipleMode: {
		0: nil,
		1: {"skip"},
		2: {"run"},
		3: {"fail"},
	},
}

func TestMain(m *testing.M) {
	if isFakeGoInvocation(os.Args[0]) {
		os.Exit(runFakeGo())
	}
	if os.Getenv(cliModeEnv) == "1" {
		os.Exit(runCLI(os.Args[1:]))
	}
	os.Exit(m.Run())
}

func runFakeGo() int {
	if !isExpectedGoCommand(os.Args[1:]) {
		reportf(os.Stderr, "unexpected go arguments: %s\n", strings.Join(os.Args[1:], " "))
		return 91
	}
	emitFakeTestEvents(os.Getenv(fakeModeEnv))
	reportf(os.Stderr, "fake Go stderr\n")
	return fakeGoExitCode()
}

func isFakeGoInvocation(path string) bool {
	base := filepath.Base(path)
	return base == fakeGoBasename || base == fakeGoBasename+".exe"
}

func isExpectedGoCommand(args []string) bool {
	expected := []string{
		"test", "-race", "-json", "-count=1", "-timeout", "10m",
		"./pkg/workspace", "./pkg/provider", "-run", providerUpdateRunPattern,
	}
	return equalStrings(args, expected)
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func emitFakeTestEvents(mode string) {
	for index, name := range criticalTests {
		if actions, except := fakeCaseActions(mode, index); except {
			for _, action := range actions {
				writeFakeEvent(action, name, "")
			}
			continue
		}
		writeFakeEvent("run", name, "")
		writeFakeEvent("pass", name, "")
	}
	emitLargeFakeLine()
}

func fakeCaseActions(mode string, index int) ([]string, bool) {
	actions, ok := fakeCaseExceptions[mode][index]
	return actions, ok
}

func emitLargeFakeLine() {
	if os.Getenv(largeLineEnv) != "1" {
		return
	}
	writeFakeEvent(
		"output", "TestProviderUpdateZipCompatibility/plain_archive", strings.Repeat("x", 70*1024),
	)
}

func fakeGoExitCode() int {
	code := os.Getenv(fakeExitEnv)
	if code == "" {
		return 0
	}
	var exitCode int
	if _, err := fmt.Sscan(code, &exitCode); err != nil {
		return 92
	}
	return exitCode
}

func writeFakeEvent(action, name, output string) {
	if output == "" {
		reportf(os.Stdout, "{\"Action\":%q,\"Test\":%q}\n", action, name)
		return
	}
	reportf(os.Stdout, "{\"Action\":%q,\"Test\":%q,\"Output\":%q}\n", action, name, output)
}

func TestValidateCriticalTestsReportsEveryIssue(t *testing.T) {
	events := make(map[string]testState)
	for _, name := range criticalTests[4:] {
		events[name] = testState{passed: true}
	}
	events[criticalTests[1]] = testState{skipped: true}
	events[criticalTests[2]] = testState{}
	events[criticalTests[3]] = testState{failed: true}

	issues := validateCriticalTests(events)
	if len(issues) != 4 {
		t.Fatalf("got %d issues, want 4: %v", len(issues), issues)
	}
	for _, want := range []string{
		"produced no JSON events: " + criticalTests[0],
		"was skipped: " + criticalTests[1],
		"did not pass: " + criticalTests[2],
		"failed: " + criticalTests[3],
	} {
		if !containsIssue(issues, want) {
			t.Errorf("issues do not contain %q: %v", want, issues)
		}
	}
}

func TestCLIStreamsAndRetainsArtifacts(t *testing.T) {
	dir := t.TempDir()
	setUpFakeGo(t, "success", "0")
	t.Setenv(largeLineEnv, "1")

	stdout, stderr, code := runCLIProcess(t, "--artifacts-dir", dir)
	if code != 0 || stdout == "" {
		t.Fatalf("expected streamed output; stdout=%q stderr=%q", stdout, stderr)
	}
	if !strings.Contains(stderr, "fake Go stderr") {
		t.Fatalf("stderr was not streamed: %q", stderr)
	}
	log, err := os.ReadFile(filepath.Join(dir, defaultArtifactName+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(log, []byte(strings.Repeat("x", 70*1024))) {
		t.Fatal("large JSON line was not retained in the artifact")
	}
	exitCode, err := os.ReadFile(filepath.Join(dir, defaultArtifactName+".exitcode"))
	if err != nil || string(exitCode) != "0" {
		t.Fatalf("exit-code artifact = %q, err=%v; want 0", exitCode, err)
	}
}

func TestCLIGateReportsAllFailures(t *testing.T) {
	for _, mode := range []string{missingMode, skipMode, noPassMode, multipleMode} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			setUpFakeGo(t, mode, "0")
			_, stderr, code := runCLIProcess(t, "--artifacts-dir", dir)
			if code == 0 {
				t.Fatal("CLI succeeded despite missing or invalid critical tests")
			}
			assertGateMode(t, mode, stderr)
		})
	}
}

func assertGateMode(t *testing.T, mode, stderr string) {
	t.Helper()
	switch mode {
	case multipleMode:
		assertMultipleGateIssues(t, stderr)
	case missingMode:
		assertMissingGateIssues(t, stderr)
	default:
		if !strings.Contains(stderr, "::error::") {
			t.Fatalf("no validation issue reported: %s", stderr)
		}
	}
}

func assertMultipleGateIssues(t *testing.T, stderr string) {
	t.Helper()
	for _, issue := range criticalTests[:4] {
		if !strings.Contains(stderr, issue) {
			t.Errorf("missing issue for %q in %s", issue, stderr)
		}
	}
	if strings.Count(stderr, "::error::") != 4 {
		t.Errorf("reported %d errors, want 4: %s", strings.Count(stderr, "::error::"), stderr)
	}
}

func assertMissingGateIssues(t *testing.T, stderr string) {
	t.Helper()
	for _, name := range criticalTests[:2] {
		if !strings.Contains(stderr, "produced no JSON events: "+name) {
			t.Errorf("missing report for %q: %s", name, stderr)
		}
	}
}

func TestCLIPreservesNativeGoExitCodeAndArtifacts(t *testing.T) {
	dir := t.TempDir()
	setUpFakeGo(t, "success", "7")
	stdout, stderr, code := runCLIProcess(t, "--artifacts-dir", dir)
	if !strings.Contains(stdout, "\"Action\":\"pass\"") ||
		!strings.Contains(stderr, "fake Go stderr") {
		t.Fatalf("child output was not streamed: stdout=%q stderr=%q", stdout, stderr)
	}
	exitArtifact, err := os.ReadFile(filepath.Join(dir, defaultArtifactName+".exitcode"))
	if err != nil || string(exitArtifact) != "7" {
		t.Fatalf("exit-code artifact = %q, err=%v; want 7", exitArtifact, err)
	}
	jsonArtifact, err := os.ReadFile(filepath.Join(dir, defaultArtifactName+".jsonl"))
	if err != nil || !bytes.Contains(jsonArtifact, []byte(`"Action":"pass"`)) {
		t.Fatalf("JSON artifact did not retain child output: err=%v", err)
	}
	if code != 7 {
		t.Fatalf("CLI exit code = %d, want native Go code 7", code)
	}
}

func TestCaptureFailureIsReportedAndDoesNotStopDrain(t *testing.T) {
	dir := t.TempDir()
	setUpFakeGo(t, "success", "0")
	var stderr bytes.Buffer
	code := runProviderUpdateTests(dir, failingWriter{}, &stderr)
	if code == 0 {
		t.Fatal("capture failure should fail the helper")
	}
	if !strings.Contains(stderr.String(), "capture Go test output") {
		t.Fatalf("capture failure was not reported: %q", stderr.String())
	}
	if strings.Count(stderr.String(), "capture Go test output:") != 1 {
		t.Fatalf("capture errors were not bounded to the first failure: %q", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(dir, defaultArtifactName+".exitcode")); err != nil {
		t.Fatalf("child was not drained and exit artifact written: %v", err)
	}
}

func TestNativeGoExitCodeWinsOverExitArtifactFailure(t *testing.T) {
	dir := t.TempDir()
	setUpFakeGo(t, "success", "7")
	if err := os.Mkdir(filepath.Join(dir, defaultArtifactName+".exitcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := runCLIProcess(t, "--artifacts-dir", dir)
	if !strings.Contains(stderr, "write Go test exit-code artifact") {
		t.Fatalf("artifact write failure was not reported: %q", stderr)
	}
	if code != 7 {
		t.Fatalf("CLI exit code = %d, want native Go code 7", code)
	}
}

func TestGoStartFailureWritesFailureExitArtifact(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", t.TempDir())
	_, stderr, code := runCLIProcess(t, "--artifacts-dir", dir)
	if code != 1 || !strings.Contains(stderr, "start Go provider update tests") {
		t.Fatalf("unexpected start failure result: code=%d stderr=%q", code, stderr)
	}
	exitArtifact, err := os.ReadFile(filepath.Join(dir, defaultArtifactName+".exitcode"))
	if err != nil || string(exitArtifact) != "1" {
		t.Fatalf("exit-code artifact = %q, err=%v; want 1", exitArtifact, err)
	}
}

func TestDefaultArtifactsDirectoryUsesRunnerTemp(t *testing.T) {
	dir := t.TempDir()
	setUpFakeGo(t, "success", "0")
	t.Setenv("RUNNER_TEMP", dir)
	stdout, stderr, code := runCLIProcess(t)
	if code != 0 || stdout == "" || strings.Contains(stderr, "required") {
		t.Fatalf("CLI did not use RUNNER_TEMP: stdout=%q stderr=%q", stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, defaultArtifactName+".jsonl")); err != nil {
		t.Fatalf("JSON artifact missing under RUNNER_TEMP: %v", err)
	}
}

func setUpFakeGo(t *testing.T, mode, exitCode string) {
	t.Helper()
	dir := t.TempDir()
	name := fakeGoBasename
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := copyFile(binary, filepath.Join(dir, name)); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(fakeModeEnv, mode)
	t.Setenv(fakeExitEnv, exitCode)
}

func runCLIProcess(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, args...)
	command.Env = append(os.Environ(), cliModeEnv+"=1")
	var capturedOut, capturedErr bytes.Buffer
	command.Stdout = &capturedOut
	command.Stderr = &capturedErr
	err = command.Run()
	code := 0
	if err != nil {
		exitErr, ok := errors.AsType[*exec.ExitError](err)
		if !ok {
			t.Fatalf("run helper CLI: %v", err)
		}
		code = exitErr.ExitCode()
	}
	return capturedOut.String(), capturedErr.String(), code
}

func copyFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	inputCloseErr := input.Close()
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	if inputCloseErr != nil {
		return inputCloseErr
	}
	return closeErr
}

func containsIssue(issues []string, fragment string) bool {
	for _, issue := range issues {
		if strings.Contains(issue, fragment) {
			return true
		}
	}
	return false
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("intentional writer failure")
}
