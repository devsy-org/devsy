package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
)

const (
	installLinuxCommand = "install-linux"
	runSuiteCommand     = "run"
	verifyReportCommand = "verify-report"
)

func main() {
	os.Exit(runCLI(os.Args[1:], os.Stdout, os.Stderr))
}

func runCLI(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(
			stderr,
			"usage: devsy-ci-microsandbox <install-linux|run|verify-report>",
		)
		return 2
	}
	switch args[0] {
	case installLinuxCommand:
		return installLinuxCLI(args[1:], stdout, stderr)
	case runSuiteCommand:
		return runSuiteCLI(args[1:], stdout, stderr)
	case verifyReportCommand:
		return verifyReportCLI(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown command: %s\n", args[0])
		return 2
	}
}

func installLinuxCLI(args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 || runtime.GOOS != "linux" {
		_, _ = fmt.Fprintln(stderr, "install-linux requires Linux and takes no arguments")
		return 2
	}
	output := commandOutput{stdout: stdout, stderr: stderr}
	err := installLinux(context.Background(), commandRunner((*exec.Cmd).Run), output)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "::error::%v\n", err)
	}
	return commandExitCode(err)
}

func runSuiteCLI(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(runSuiteCommand, flag.ContinueOnError)
	flags.SetOutput(stderr)
	cfg := suiteConfig{}
	flags.StringVar(&cfg.label, "label", "", "Ginkgo suite label")
	flags.IntVar(&cfg.expectedCount, "expected-count", 0, "required scenario count")
	flags.StringVar(&cfg.repositoryDir, "repository-dir", ".", "Devsy checkout directory")
	flags.StringVar(
		&cfg.tempDir,
		"artifacts-dir",
		os.Getenv("RUNNER_TEMP"),
		"runner artifact directory",
	)
	flags.StringVar(
		&cfg.testTimeout,
		"test-timeout",
		os.Getenv("TEST_TIMEOUT"),
		"Ginkgo test deadline",
	)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "run does not accept positional arguments")
		return 2
	}
	return runSuite(
		context.Background(),
		cfg,
		commandRunner((*exec.Cmd).Run),
		commandOutput{stdout: stdout, stderr: stderr},
	)
}

func verifyReportCLI(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(verifyReportCommand, flag.ContinueOnError)
	flags.SetOutput(stderr)
	report := flags.String("report", "", "Ginkgo JSON report path")
	label := flags.String("label", "", "Ginkgo suite label")
	count := flags.Int("expected-count", 0, "required scenario count")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || !validReportArguments(*report, *label, *count) {
		_, _ = fmt.Fprintln(
			stderr,
			"verify-report requires a report, label and positive even expected-count",
		)
		return 2
	}
	if err := validateReport(*report, *label, *count); err != nil {
		_, _ = fmt.Fprintf(stderr, "::error::%v\n", err)
		return 1
	}
	if _, err := fmt.Fprintf(
		stdout,
		"%s: %d specs passed in one attempt each\n",
		*label,
		*count,
	); err != nil {
		return 1
	}
	return 0
}

func validReportArguments(report, label string, count int) bool {
	return report != "" && label != "" && isPositiveEven(count)
}
