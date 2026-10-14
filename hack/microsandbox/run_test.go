package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestRunSuiteCapturesOutputAndPreservesInvocationContract(t *testing.T) {
	cfg, tempDir := newSuiteRunFixture(t, "up-provider-microsandbox-images")
	var console, stderr bytes.Buffer
	var commands [][]string
	run := func(cmd *exec.Cmd) error {
		commands = append(commands, append([]string(nil), cmd.Args...))
		if suiteCommandIsChown(cmd) {
			return nil
		}
		if cmd.Dir != filepath.Join(cfg.repositoryDir, "e2e") {
			t.Errorf("working directory = %q", cmd.Dir)
		}
		if err := writeSuiteReportFromCommand(cmd); err != nil {
			return err
		}
		_, _ = io.WriteString(cmd.Stdout, "fixture stdout\n")
		_, _ = io.WriteString(cmd.Stderr, "fixture stderr\n")
		return nil
	}

	if code := runSuite(
		context.Background(),
		cfg,
		run,
		commandOutput{stdout: &console, stderr: &stderr},
	); code != 0 {
		t.Fatalf("runSuite() = %d; stderr=%s", code, stderr.String())
	}
	if len(commands) != 2 || !suiteCommandIsChownArgs(commands[1]) {
		t.Fatalf("commands = %#v", commands)
	}
	command := commands[0]
	assertSuiteInvocation(t, command, cfg, tempDir)
	assertSuiteOutput(t, console.String(), stderr.String(), cfg)
}

func assertSuiteInvocation(t *testing.T, command []string, cfg suiteConfig, tempDir string) {
	t.Helper()
	expectedCommand := []string{
		"./e2e.test", "-test.v", "-ginkgo.v", "-test.timeout", cfg.testTimeout,
		"-ginkgo.fail-on-empty", "-ginkgo.flake-attempts=1",
		"--ginkgo.label-filter=" + cfg.label,
		"-ginkgo.json-report=" + filepath.Join(tempDir, "microsandbox-"+cfg.label+".json"),
	}
	start := slices.Index(command, "./e2e.test")
	if start < 0 || !slices.Equal(command[start:], expectedCommand) {
		t.Fatalf("test command args = %v, want suffix %v", command, expectedCommand)
	}
	expectedEnvironment := []string{
		"DEVSY_REQUIRE_MICROSANDBOX=true", "GH_USERNAME=owner", "GH_ACCESS_TOKEN=never-print-token",
		"GH_CREDENTIAL_USERNAME=x-access-token", "PATH=/bin", "KUBECONFIG=/tmp/kubeconfig",
		"DOCKER_HOST=unix:///tmp/docker.sock",
	}
	if !slices.Equal(command[1:start], expectedEnvironment) {
		t.Fatalf("sudo environment = %v, want exactly %v", command[1:start], expectedEnvironment)
	}
	if slices.Contains(command[1:start], "UNRELATED_SECRET=not-forwarded") {
		t.Fatal("unrelated secret was forwarded to sudo")
	}
}

func assertSuiteOutput(t *testing.T, console, stderr string, cfg suiteConfig) {
	t.Helper()
	log, err := os.ReadFile(filepath.Join(cfg.tempDir, "microsandbox-"+cfg.label+".log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "fixture stdout") ||
		!strings.Contains(string(log), "fixture stderr") ||
		!strings.Contains(console, "fixture stdout") ||
		!strings.Contains(console, "fixture stderr") {
		t.Fatalf("combined output missing from log or console: log=%q console=%q", log, console)
	}
	if strings.Contains(console+stderr+string(log), "never-print-token") {
		t.Fatal("test credential leaked to output")
	}
	if !strings.Contains(console, cfg.label+": 2 specs passed in one attempt each") {
		t.Fatalf("success validation message missing: %q", console)
	}
}

func TestRunSuiteManagedPolicyUsesWrapperAndFixtureEnvironment(t *testing.T) {
	cfg, tempDir := newSuiteRunFixture(t, managedPolicyLabel)
	var commands [][]string
	run := func(cmd *exec.Cmd) error {
		commands = append(commands, append([]string(nil), cmd.Args...))
		if suiteCommandIsChown(cmd) {
			return nil
		}
		return writeSuiteReportFromCommand(cmd)
	}
	var stdout, stderr bytes.Buffer
	if code := runSuite(
		context.Background(),
		cfg,
		run,
		commandOutput{stdout: &stdout, stderr: &stderr},
	); code != 0 {
		t.Fatalf("runSuite() = %d; stderr=%s", code, stderr.String())
	}
	if len(commands) != 2 || !stringArgsContain(commands[0], "../hack/run-managed-policy-e2e.sh") {
		t.Fatalf("managed invocation = %#v", commands)
	}
	for _, want := range []string{
		"DEVSY_REQUIRE_MSB_MANAGED_POLICY_FIXTURE=true", "RUNNER_TEMP=" + tempDir,
	} {
		if !stringArgsContain(commands[0], want) {
			t.Errorf("managed invocation lacks %q: %v", want, commands[0])
		}
	}
}

func TestRunSuitePreservesNativeExitPrecedenceAndArtifacts(t *testing.T) {
	cfg, tempDir := newSuiteRunFixture(t, "up-provider-microsandbox-images")
	var console, stderr bytes.Buffer
	run := func(cmd *exec.Cmd) error {
		if suiteCommandIsChown(cmd) {
			return nil
		}
		if err := writeSuiteReportFromCommand(cmd); err != nil {
			return err
		}
		_, _ = io.WriteString(cmd.Stdout, "retained output\n")
		return exitWithStatus(23)
	}
	code := runSuite(
		context.Background(),
		cfg,
		run,
		commandOutput{stdout: &console, stderr: &stderr},
	)
	if code != 23 {
		t.Fatalf("runSuite() = %d, want native failure status 23", code)
	}
	assertArtifactsRetained(t, tempDir, cfg.label)
	assertNativeFailureReported(t, stderr.String(), console.String(), cfg.label)
}

func assertArtifactsRetained(t *testing.T, tempDir, label string) {
	t.Helper()
	for _, name := range []string{
		"microsandbox-" + label + ".json",
		"microsandbox-" + label + ".log",
	} {
		if info, err := os.Stat(filepath.Join(tempDir, name)); err != nil || info.IsDir() {
			t.Errorf("artifact %s not retained: info=%v err=%v", name, info, err)
		}
	}
}

func assertNativeFailureReported(t *testing.T, stderr, console, label string) {
	t.Helper()
	if !strings.Contains(stderr, "test command failed") {
		t.Fatalf("native failure was not recorded: %q", stderr)
	}
	if !strings.Contains(console, label+": 2 specs passed in one attempt each") {
		t.Fatalf("readable report was not validated after test failure: %q", console)
	}
}

func TestRunSuiteReportsMissingReportAfterNativeFailure(t *testing.T) {
	cfg, _ := newSuiteRunFixture(t, "up-provider-microsandbox-images")
	var stderr bytes.Buffer
	run := func(cmd *exec.Cmd) error {
		if suiteCommandIsChown(cmd) {
			t.Fatal("chown ran without a report")
		}
		return exitWithStatus(23)
	}
	if code := runSuite(
		context.Background(),
		cfg,
		run,
		commandOutput{stderr: &stderr},
	); code != 23 {
		t.Fatalf("runSuite() = %d", code)
	}
	if !strings.Contains(stderr.String(), "cannot read Ginkgo JSON report") ||
		!strings.Contains(stderr.String(), "test command failed") {
		t.Fatalf("native and report failures not both retained: %q", stderr.String())
	}
}

func TestRunSuiteReturnsReportErrorsAfterSuccessfulE2E(t *testing.T) {
	t.Run("missing report", func(t *testing.T) {
		cfg, _ := newSuiteRunFixture(t, "up-provider-microsandbox-images")
		var stderr bytes.Buffer
		run := func(cmd *exec.Cmd) error {
			if suiteCommandIsChown(cmd) {
				t.Fatal("chown ran without a report")
			}
			return nil
		}
		if code := runSuite(
			context.Background(),
			cfg,
			run,
			commandOutput{stderr: &stderr},
		); code != 1 {
			t.Fatalf("runSuite() = %d", code)
		}
		if !strings.Contains(stderr.String(), "cannot read Ginkgo JSON report") {
			t.Fatalf("missing report error = %q", stderr.String())
		}
	})
	t.Run("report chown failure", func(t *testing.T) {
		cfg, _ := newSuiteRunFixture(t, "up-provider-microsandbox-images")
		var stderr bytes.Buffer
		run := func(cmd *exec.Cmd) error {
			if suiteCommandIsChown(cmd) {
				return exitWithStatus(43)
			}
			return writeSuiteReportFromCommand(cmd)
		}
		if code := runSuite(
			context.Background(),
			cfg,
			run,
			commandOutput{stderr: &stderr},
		); code != 43 {
			t.Fatalf("runSuite() = %d, want chown exit status 43", code)
		}
		if !strings.Contains(stderr.String(), "could not make Ginkgo report readable") {
			t.Fatalf("chown failure not reported: %q", stderr.String())
		}
	})
}

func TestRunSuiteContinuesAfterOutputLogFailure(t *testing.T) {
	cfg, tempDir := newSuiteRunFixture(t, "up-provider-microsandbox-images")
	if err := os.Mkdir(
		filepath.Join(tempDir, "microsandbox-"+cfg.label+".log"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	var console, stderr bytes.Buffer
	var testStarted bool
	run := func(cmd *exec.Cmd) error {
		if suiteCommandIsChown(cmd) {
			return nil
		}
		testStarted = true
		if err := writeSuiteReportFromCommand(cmd); err != nil {
			return err
		}
		_, _ = io.WriteString(cmd.Stdout, "console remains live\n")
		return nil
	}
	code := runSuite(
		context.Background(),
		cfg,
		run,
		commandOutput{stdout: &console, stderr: &stderr},
	)
	if code == 0 || !testStarted || !strings.Contains(console.String(), "console remains live") {
		t.Fatalf("runSuite() = %d, started=%t, console=%q", code, testStarted, console.String())
	}
	if !strings.Contains(stderr.String(), "could not capture MicroSandbox test output") {
		t.Fatalf("capture failure not reported: %q", stderr.String())
	}
}

func TestRunSuiteOutputWriterFailureDoesNotMaskNativeExit(t *testing.T) {
	cfg, _ := newSuiteRunFixture(t, "up-provider-microsandbox-images")
	var stderr bytes.Buffer
	run := func(cmd *exec.Cmd) error {
		if suiteCommandIsChown(cmd) {
			return nil
		}
		if err := writeSuiteReportFromCommand(cmd); err != nil {
			return err
		}
		_, _ = io.WriteString(cmd.Stdout, "writer error")
		return exitWithStatus(23)
	}
	code := runSuite(
		context.Background(),
		cfg,
		run,
		commandOutput{stdout: failingWriter{}, stderr: &stderr},
	)
	if code != 23 || !strings.Contains(stderr.String(), "test command failed") {
		t.Fatalf("runSuite() = %d, stderr=%q", code, stderr.String())
	}
}

func TestRunSuiteReportsMissingRequiredEnvironmentBeforeInvocation(t *testing.T) {
	cfg, _ := newSuiteRunFixture(t, "up-provider-microsandbox-images")
	t.Setenv("GH_USERNAME", "")
	var stderr bytes.Buffer
	run := func(*exec.Cmd) error {
		t.Fatal("test command ran with missing required environment")
		return nil
	}
	if code := runSuite(context.Background(), cfg, run, commandOutput{stderr: &stderr}); code != 2 {
		t.Fatalf("runSuite() = %d", code)
	}
	if !strings.Contains(stderr.String(), "GH_USERNAME must be set") {
		t.Fatalf("missing environment error = %q", stderr.String())
	}
}

func newSuiteRunFixture(t *testing.T, label string) (suiteConfig, string) {
	t.Helper()
	tempDir := t.TempDir()
	repository := t.TempDir()
	if err := os.Mkdir(filepath.Join(repository, "e2e"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVSY_REQUIRE_MICROSANDBOX", "true")
	t.Setenv("GH_USERNAME", "owner")
	t.Setenv("GH_ACCESS_TOKEN", "never-print-token")
	t.Setenv("GH_CREDENTIAL_USERNAME", "x-access-token")
	t.Setenv("KUBECONFIG", "/tmp/kubeconfig")
	t.Setenv("PATH", "/bin")
	t.Setenv("DOCKER_HOST", "unix:///tmp/docker.sock")
	t.Setenv("UNRELATED_SECRET", "not-forwarded")
	t.Setenv("HOME", t.TempDir())
	return suiteConfig{
		label: label, expectedCount: 2, repositoryDir: repository,
		tempDir: tempDir, testTimeout: "1200s",
	}, tempDir
}

func writeSuiteReportFromCommand(cmd *exec.Cmd) error {
	var reportPath, label string
	for _, arg := range cmd.Args {
		if value, ok := strings.CutPrefix(arg, "-ginkgo.json-report="); ok {
			reportPath = value
		}
		if value, ok := strings.CutPrefix(arg, "--ginkgo.label-filter="); ok {
			label = value
		}
	}
	if reportPath == "" || label == "" {
		return errors.New("Ginkgo report or label argument missing")
	}
	data, err := json.Marshal(validReportFor(label))
	if err != nil {
		return err
	}
	return os.WriteFile(reportPath, data, 0o600)
}

func suiteCommandIsChown(cmd *exec.Cmd) bool {
	return suiteCommandIsChownArgs(cmd.Args)
}

func suiteCommandIsChownArgs(args []string) bool {
	return len(args) > 1 && args[1] == "chown"
}

func stringArgsContain(args []string, want string) bool {
	return slices.Contains(args, want)
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("fixture writer failure") }

func exitWithStatus(status int) error {
	return exec.Command("sh", "-c", "exit "+strconv.Itoa(status)).Run()
}
