package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestExecRunnerCapturesOutputAndExitCode(t *testing.T) {
	t.Setenv("DEVSY_PODMAN_RUNNER_HELPER", "short")
	result := (&ExecRunner{}).Run(
		context.Background(),
		CommandSpec{
			Path:    os.Args[0],
			Args:    []string{"-test.v", "-test.run=TestRunnerHelperProcess"},
			Timeout: 5 * time.Second,
		},
	)
	if !result.Success() || !strings.Contains(result.Stdout, "runner helper") {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestExitCodeString(t *testing.T) {
	if got := exitCodeString(nil); got != "none" {
		t.Fatalf("nil exit code = %q, want none", got)
	}
	code := 124
	if got := exitCodeString(&code); got != "124" {
		t.Fatalf("exit code = %q, want 124", got)
	}
}

func TestExecRunnerTimesOutDirectChild(t *testing.T) {
	t.Setenv("DEVSY_PODMAN_RUNNER_HELPER", "sleep")
	result := (&ExecRunner{}).Run(
		context.Background(),
		CommandSpec{
			Path:    os.Args[0],
			Args:    []string{"-test.run=TestRunnerHelperProcess"},
			Timeout: 30 * time.Millisecond,
		},
	)
	if !result.TimedOut || result.ExitCode != nil {
		t.Fatalf("expected timeout result, got %+v", result)
	}
}

func TestRunnerHelperProcess(_ *testing.T) {
	switch os.Getenv("DEVSY_PODMAN_RUNNER_HELPER") {
	case "":
		return
	case "sleep":
		time.Sleep(10 * time.Second)
	default:
		_, _ = os.Stdout.WriteString("runner helper\n")
	}
}
