package subprocess

import (
	"bytes"
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/secrets"
)

type failingWriter struct{ calls int }

func (w *failingWriter) Write([]byte) (int, error) {
	w.calls++
	return 0, errors.New("sink unavailable")
}

func skipWithoutShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == testWindows {
		t.Skip("test command uses sh")
	}
}

func TestDiagnosticSinkReceivesFullOutputWhileResultStaysBounded(t *testing.T) {
	skipWithoutShell(t)
	lines := MaxCapturedLines * 3
	var out bytes.Buffer
	result, err := Run(context.Background(), "sh",
		[]string{"-c", "i=0; while [ $i -lt 600 ]; do echo line-$i; i=$((i+1)); done"},
		Options{Diagnostic: DiagnosticSink{Stdout: &out}})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(out.String(), "\n"); got != lines {
		t.Fatalf("sink received %d lines, want %d", got, lines)
	}
	if !strings.HasPrefix(out.String(), "line-0\n") {
		t.Fatal("sink lost the head of the output")
	}
	if !result.Truncated || strings.Count(result.Stdout, "\n") > MaxCapturedLines {
		t.Fatal("result must keep its bounded tail")
	}
}

func TestDiagnosticSinkKeepsStreamsSeparateAndFlushesFragment(t *testing.T) {
	skipWithoutShell(t)
	var out, errOut bytes.Buffer
	_, err := Run(context.Background(), "sh",
		[]string{"-c", "printf 'to-out\\n'; printf 'to-err\\n' >&2; printf 'no-newline'"},
		Options{Diagnostic: DiagnosticSink{Stdout: &out, Stderr: &errOut}})
	if err != nil {
		t.Fatal(err)
	}
	if out.String() != "to-out\nno-newline" {
		t.Fatalf("stdout sink = %q", out.String())
	}
	if errOut.String() != "to-err\n" {
		t.Fatalf("stderr sink = %q", errOut.String())
	}
}

func TestDiagnosticSinkMasksSecretSplitAcrossWrites(t *testing.T) {
	skipWithoutShell(t)
	var out bytes.Buffer
	script := "printf 'key=DEVSY_SECRET_TEST_'; sleep 0.1; printf '846297 done\\n'"
	_, err := Run(context.Background(), "sh", []string{"-c", script}, Options{
		Redactor:   secrets.NewRedactor([]string{captureToken + captureTestSecret}),
		Diagnostic: DiagnosticSink{Stdout: &out},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "846297") || !strings.Contains(out.String(), "done") {
		t.Fatalf("sink output = %q, want secret masked and text kept", out.String())
	}
}

func TestDiagnosticSinkFailureDoesNotChangeOutcome(t *testing.T) {
	skipWithoutShell(t)
	sink := &failingWriter{}
	result, err := Run(context.Background(), "sh", []string{"-c", "echo hello"},
		Options{Diagnostic: DiagnosticSink{Stdout: sink}})
	if err != nil || result.ExitCode != 0 || result.Stdout != "hello\n" {
		t.Fatalf("result = %+v, err = %v; a failing sink must not alter the command", result, err)
	}
	if sink.calls == 0 {
		t.Fatal("sink was never written")
	}
}

func TestDiagnosticSinkPreservesExitCodeAndFunctionalStdout(t *testing.T) {
	skipWithoutShell(t)
	var protocol, sink bytes.Buffer
	result, err := Run(
		context.Background(),
		"sh",
		[]string{"-c", "printf 'a\\nb'; exit 7"},
		Options{
			Stdout:     &protocol,
			Diagnostic: DiagnosticSink{Stdout: &sink},
		},
	)
	if err == nil || result.ExitCode != 7 {
		t.Fatalf("result = %+v, err = %v, want exit 7", result, err)
	}
	if protocol.String() != "a\nb" || sink.String() != "a\nb" {
		t.Fatalf("protocol = %q, sink = %q", protocol.String(), sink.String())
	}
}

func TestDiagnosticSinkOnStartFailureAndCancellation(t *testing.T) {
	var sink bytes.Buffer
	opts := Options{Diagnostic: DiagnosticSink{Stdout: &sink, Stderr: &sink}}
	if _, err := Run(
		context.Background(),
		"definitely-not-a-binary-846297",
		nil,
		opts,
	); err == nil {
		t.Fatal("start failure should return an error")
	}
	skipWithoutShell(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	result, err := Run(ctx, "sh", []string{"-c", "echo before; sleep 5"}, opts)
	if !errors.Is(err, context.DeadlineExceeded) || result.Signal == "" {
		t.Fatalf("result = %+v, err = %v, want a canceled signaled command", result, err)
	}
	if !strings.Contains(sink.String(), "before") {
		t.Fatalf("sink = %q, want output written before cancellation", sink.String())
	}
}
