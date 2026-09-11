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
	"github.com/devsy-org/devsy/pkg/status"
)

func TestBoundedBufferRedactsAndCapsLines(t *testing.T) {
	b := NewBuffer(secrets.NewRedactor([]string{"TOKEN=DEVSY_SECRET_TEST_846297"})) //nolint:gosec,goconst // test-only redaction fixture
	for range MaxCapturedLines + 5 {
		_, _ = b.Write([]byte("DEVSY_SECRET_TEST_846297\n"))
	}
	if strings.Contains(b.String(), "DEVSY_SECRET_TEST_846297") {
		t.Fatal("secret escaped bounded buffer")
	}
	if !b.Truncated() {
		t.Fatal("buffer should report truncation")
	}
	if got := strings.Count(b.String(), "\n"); got > MaxCapturedLines {
		t.Fatalf("captured %d lines, want at most %d", got, MaxCapturedLines)
	}
	if !strings.HasSuffix(b.String(), "***\n") {
		t.Fatalf("buffer did not retain the useful tail: %q", b.String()[max(0, len(b.String())-80):])
	}
}

func TestDisplayCommandRedactsAndQuotes(t *testing.T) {
	redactor := secrets.NewRedactor([]string{"TOKEN=DEVSY_SECRET_TEST_846297"})
	got := displayCommand("docker", []string{"run", "token=DEVSY_SECRET_TEST_846297", "hello world"}, redactor)
	want := `docker run token=*** "hello world"`
	if got != want {
		t.Errorf("display command = %q, want %q", got, want)
	}
}

func TestRedactingWriterRedactsStreamedOutput(t *testing.T) {
	var out bytes.Buffer
	w := &StreamingRedactingWriter{Next: &out, Redactor: secrets.NewRedactor([]string{"TOKEN=DEVSY_SECRET_TEST_846297"})}
	_, _ = w.Write([]byte("token=DEVSY_SECRET_TEST_846297"))
	_ = w.Flush()
	if got := out.String(); got != "token=***" {
		t.Errorf("streamed output = %q, want %q", got, "token=***")
	}
}

func TestRedactingWriterRedactsSecretSplitAcrossWrites(t *testing.T) {
	var out bytes.Buffer
	w := &StreamingRedactingWriter{
		Next:     &out,
		Redactor: secrets.NewRedactor([]string{"TOKEN=DEVSY_SECRET_TEST_846297"}),
	}
	secret := "DEVSY_SECRET_TEST_846297" //nolint:gosec,goconst // test-only redaction fixture
	_, _ = w.Write([]byte("token=" + secret[:7]))
	_, _ = w.Write([]byte(secret[7:]))
	_ = w.Flush()
	if got := out.String(); strings.Contains(got, secret) || got != "token=***" {
		t.Errorf("split streamed output = %q, want %q", got, "token=***")
	}
}

func TestBoundedBufferRedactsSecretSplitAcrossWrites(t *testing.T) {
	secret := "DEVSY_SECRET_TEST_846297" //nolint:gosec,goconst // test-only redaction fixture
	b := NewBuffer(secrets.NewRedactor([]string{"TOKEN=" + secret}))
	_, _ = b.Write([]byte("prefix=" + secret[:8]))
	_, _ = b.Write([]byte(secret[8:] + " suffix"))

	got := b.String()
	if strings.Contains(got, secret) {
		t.Fatalf("split secret escaped bounded buffer: %q", got)
	}
	if !strings.Contains(got, "prefix=***") {
		t.Fatalf("split secret was not masked: %q", got)
	}
}

func TestRunCapturesFailureOutput(t *testing.T) {
	if runtime.GOOS == "windows" { //nolint:goconst // platform branch is explicit
		t.Skip("test command uses sh")
	}
	result, err := Run(context.Background(), "sh", []string{"-c", "printf out; printf err >&2; exit 7"}, Options{})
	if err == nil {
		t.Fatal("Run succeeded, want failure")
	}
	if result.ExitCode != 7 || result.Stdout != "out" || result.Stderr != "err" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if !strings.Contains(result.DisplayCommand, "sh -c") {
		t.Errorf("display command = %q", result.DisplayCommand)
	}
}

func TestRunRedactsSensitiveEnvironmentAndArgvValues(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test command uses sh")
	}
	secretFromEnv := "DEVSY_SECRET_TEST_846297" //nolint:gosec,goconst // test-only redaction fixture
	secretFromArgs := "DEVSY_ARG_SECRET_846298" //nolint:gosec // test-only redaction fixture
	result, err := Run(context.Background(), "sh", []string{"-c", "printf '%s %s' \"$TOKEN\" \"$1\"", "sh", secretFromArgs}, Options{
		Env:             []string{"TOKEN=" + secretFromEnv},
		SensitiveValues: []string{secretFromArgs},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for name, value := range map[string]string{
		"env":  secretFromEnv,
		"argv": secretFromArgs,
	} {
		if strings.Contains(result.Stdout+result.DisplayCommand, value) {
			t.Fatalf("%s secret escaped subprocess result: %+v", name, result)
		}
	}
	if !strings.Contains(result.Stdout, "*** ***") {
		t.Fatalf("redacted output = %q, want both values masked", result.Stdout)
	}
}

func TestRunCapturesSignal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test command uses sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	result, err := Run(ctx, "sh", []string{"-c", "sleep 5"}, Options{})
	if err == nil {
		t.Fatal("Run succeeded, want timeout")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run error = %v, want context.DeadlineExceeded", err)
	}
	if result.Signal == "" {
		t.Fatalf("missing signal in result: %+v", result)
	}
}

func TestDiagnosticOutputAnnotatesTruncatedTail(t *testing.T) {
	result := Result{
		Stdout:    "last useful line",
		Stderr:    "warning",
		Truncated: true,
	}

	got := result.DiagnosticOutput()
	if !strings.Contains(got, "warning\nlast useful line") {
		t.Fatalf("diagnostic output = %q, want stderr before stdout", got)
	}
	if !strings.Contains(got, "additional output omitted") || !strings.Contains(got, "--verbose") {
		t.Fatalf("diagnostic output = %q, want truncation guidance", got)
	}
}

func TestRunAssociatesActiveOperation(t *testing.T) {
	var got Result
	err := status.Run(context.Background(), status.Nop(), status.Operation{Phase: status.PhaseBuildingImage}, func(ctx context.Context) error {
		var err error
		got, err = Run(ctx, "sh", []string{"-c", "exit 0"}, Options{})
		return err
	})
	if err != nil {
		t.Fatalf("status.Run: %v", err)
	}
	if got.OperationID == "" {
		t.Fatalf("subprocess result has no operation ID: %+v", got)
	}
}
