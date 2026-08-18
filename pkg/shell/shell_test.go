package shell

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestRunEmulatedShell_KillExecutesRealBinary(t *testing.T) {
	pid := os.Getpid()
	cmd := fmt.Sprintf("kill -0 %d", pid)

	var stdout, stderr bytes.Buffer
	err := RunEmulatedShell(context.Background(), &CommandRunner{
		Command: cmd,
		Stdout:  &stdout,
		Stderr:  &stderr,
		Environ: os.Environ(),
	})
	if err != nil {
		t.Fatalf(
			"kill -0 on own PID should succeed, got error: %v\nstderr: %s",
			err,
			stderr.String(),
		)
	}
}

func TestRunEmulatedShell_ParseError(t *testing.T) {
	err := RunEmulatedShell(
		context.Background(),
		&CommandRunner{
			Command: "if then fi (((( broken syntax",
			Stdout:  &bytes.Buffer{},
			Stderr:  &bytes.Buffer{},
			Environ: os.Environ(),
		},
	)
	if err == nil {
		t.Fatal("expected parse error for malformed command, got nil")
	}
	if !strings.Contains(err.Error(), "parse shell command") {
		t.Fatalf("expected 'parse shell command' error, got: %v", err)
	}
}

func TestRunEmulatedShell_DevNullRedirect(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := RunEmulatedShell(
		context.Background(),
		&CommandRunner{
			Command: "echo suppressed > /dev/null; echo visible",
			Stdout:  &stdout,
			Stderr:  &stderr,
			Environ: os.Environ(),
		},
	)
	if err != nil {
		t.Fatalf("expected nil error, got: %v\nstderr: %s", err, stderr.String())
	}
	if stdout.String() != "visible\n" {
		t.Fatalf("expected only 'visible' on stdout, got: %q", stdout.String())
	}
}

func TestRunEmulatedShell_NilEnvFallsBackToSystem(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := RunEmulatedShell(
		context.Background(),
		&CommandRunner{
			Command: "echo $HOME",
			Stdout:  &stdout,
			Stderr:  &stderr,
			Environ: nil,
		},
	)
	if err != nil {
		t.Fatalf("expected nil error, got: %v\nstderr: %s", err, stderr.String())
	}
	home := os.Getenv("HOME")
	if home == "" {
		t.Skip("HOME not set, cannot verify env fallback")
	}
	if !strings.Contains(stdout.String(), home) {
		t.Fatalf("expected stdout to contain HOME=%q, got: %q", home, stdout.String())
	}
}
