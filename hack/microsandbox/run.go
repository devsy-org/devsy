package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
)

const managedPolicyLabel = "up-provider-microsandbox-managed-recreate"

var suiteLabelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)

func runSuite(
	ctx context.Context,
	cfg suiteConfig,
	run commandRunner,
	output commandOutput,
) int {
	output = normalizedCommandOutput(output)
	if err := validateSuiteConfig(cfg); err != nil {
		_, _ = fmt.Fprintf(output.stderr, "::error::%v\n", err)
		return 2
	}
	if run == nil {
		_, _ = fmt.Fprintln(output.stderr, "::error::command runner is required")
		return 2
	}

	execution := executeSuite(ctx, cfg, run, output)
	reportStatus := validateSuiteReport(run, cfg, output)
	if execution.testStatus != 0 {
		return execution.testStatus
	}
	if execution.captureErr != nil {
		_, _ = fmt.Fprintf(
			output.stderr,
			"::error::could not capture MicroSandbox test output: %v\n",
			execution.captureErr,
		)
		return 1
	}
	return reportStatus
}

func normalizedCommandOutput(output commandOutput) commandOutput {
	if output.stdout == nil {
		output.stdout = io.Discard
	}
	if output.stderr == nil {
		output.stderr = io.Discard
	}
	return output
}

type suiteExecution struct {
	testStatus int
	captureErr error
}

func executeSuite(
	ctx context.Context,
	cfg suiteConfig,
	run commandRunner,
	output commandOutput,
) suiteExecution {
	args, err := suiteSudoArguments(cfg)
	if err != nil {
		_, _ = fmt.Fprintf(output.stderr, "::error::%v\n", err)
		return suiteExecution{testStatus: 2}
	}

	reportPath := filepath.Join(cfg.tempDir, "microsandbox-"+cfg.label+".json")
	logPath := filepath.Join(cfg.tempDir, "microsandbox-"+cfg.label+".log")
	logFile, logOpenErr := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	var capture *suiteOutputCapture
	if logFile == nil {
		capture = &suiteOutputCapture{console: output.stdout}
		capture.record(logOpenErr)
	} else {
		capture = &suiteOutputCapture{console: output.stdout, log: logFile}
	}

	commandArgs := []string{
		"./e2e.test", "-test.v", "-ginkgo.v", "-test.timeout", cfg.testTimeout,
		"-ginkgo.fail-on-empty", "-ginkgo.flake-attempts=1",
		"--ginkgo.label-filter=" + cfg.label,
		"-ginkgo.json-report=" + reportPath,
	}
	if cfg.label == managedPolicyLabel {
		commandArgs = append([]string{"../hack/run-managed-policy-e2e.sh"}, commandArgs...)
	}
	args = append(args, commandArgs...)
	cmd := exec.CommandContext(ctx, "sudo", args...)
	cmd.Dir = filepath.Join(cfg.repositoryDir, "e2e")
	cmd.Stdout = capture
	cmd.Stderr = capture

	commandErr := run(cmd)
	testStatus := commandExitCode(commandErr)
	if commandErr != nil {
		_, _ = fmt.Fprintf(
			output.stderr,
			"::error::MicroSandbox test command failed: %v\n",
			commandErr,
		)
	}
	if logFile != nil {
		capture.record(logFile.Close())
	}
	return suiteExecution{testStatus: testStatus, captureErr: capture.err}
}

func validateSuiteConfig(cfg suiteConfig) error {
	if cfg.label == "" || !isValidSuiteLabel(cfg.label) {
		return fmt.Errorf("invalid Ginkgo label: %s", cfg.label)
	}
	if !isPositiveEven(cfg.expectedCount) {
		return errors.New("expected-spec-count must be a positive even integer")
	}
	if !directoryExists(cfg.repositoryDir) {
		return errors.New("repository directory is required")
	}
	if !directoryExists(cfg.tempDir) {
		return errors.New("RUNNER_TEMP must name an existing directory")
	}
	if cfg.testTimeout == "" {
		return errors.New("TEST_TIMEOUT must be set by the workflow")
	}
	return nil
}

func isPositiveEven(value int) bool {
	return value > 0 && value%2 == 0
}

func directoryExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func isValidSuiteLabel(label string) bool {
	return suiteLabelPattern.MatchString(label)
}

func suiteSudoArguments(cfg suiteConfig) ([]string, error) {
	names := []string{
		"DEVSY_REQUIRE_MICROSANDBOX", "GH_USERNAME", "GH_ACCESS_TOKEN",
		"GH_CREDENTIAL_USERNAME", "PATH",
	}
	values := make([]string, 0, len(names)+3)
	for _, name := range names {
		value, ok := os.LookupEnv(name)
		if !ok || value == "" {
			return nil, fmt.Errorf("%s must be set by the workflow", name)
		}
		values = append(values, name+"="+value)
	}
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		home := os.Getenv("HOME")
		if home == "" {
			return nil, errors.New("HOME must be set to resolve the default KUBECONFIG")
		}
		kubeconfig = filepath.Join(home, ".kube", "config")
	}
	values = append(values, "KUBECONFIG="+kubeconfig)
	if dockerHost := os.Getenv("DOCKER_HOST"); dockerHost != "" {
		values = append(values, "DOCKER_HOST="+dockerHost)
	}
	if cfg.label == managedPolicyLabel {
		values = append(values,
			"DEVSY_REQUIRE_MSB_MANAGED_POLICY_FIXTURE=true",
			"RUNNER_TEMP="+cfg.tempDir,
		)
	}
	return values, nil
}

func validateSuiteReport(
	run commandRunner,
	cfg suiteConfig,
	output commandOutput,
) int {
	reportPath := filepath.Join(cfg.tempDir, "microsandbox-"+cfg.label+".json")
	uidGid := strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid())
	if _, err := os.Stat(reportPath); err == nil {
		chown := exec.Command("sudo", "chown", uidGid, reportPath)
		if err := run(chown); err != nil {
			_, _ = fmt.Fprintln(
				output.stderr,
				"::error::could not make Ginkgo report readable for artifact upload",
			)
			return commandExitCode(err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		_, _ = fmt.Fprintf(output.stderr, "::error::could not inspect Ginkgo report: %v\n", err)
		return 1
	}
	if err := validateReport(reportPath, cfg.label, cfg.expectedCount); err != nil {
		_, _ = fmt.Fprintf(output.stderr, "::error::%v\n", err)
		return 1
	}
	if _, err := fmt.Fprintf(
		output.stdout,
		"%s: %d specs passed in one attempt each\n",
		cfg.label,
		cfg.expectedCount,
	); err != nil {
		return 1
	}
	return 0
}

type suiteOutputCapture struct {
	console io.Writer
	log     io.Writer
	err     error
}

func (capture *suiteOutputCapture) Write(data []byte) (int, error) {
	capture.writeTo(capture.console, data)
	capture.writeTo(capture.log, data)
	return len(data), nil
}

func (capture *suiteOutputCapture) writeTo(destination io.Writer, data []byte) {
	if destination == nil {
		return
	}
	written, err := destination.Write(data)
	if err == nil && written != len(data) {
		err = io.ErrShortWrite
	}
	capture.record(err)
}

func (capture *suiteOutputCapture) record(err error) {
	if err != nil && capture.err == nil {
		capture.err = err
	}
}
