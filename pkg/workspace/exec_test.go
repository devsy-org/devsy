package workspace

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	devcconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
)

func TestExpandWithOptions(t *testing.T) {
	t.Setenv("DEVSY_TEST_OSVAR", "from-os")
	opts := map[string]config.OptionValue{
		"DOCKER_PATH": {Value: "podman"},
		"EMPTY":       {Value: ""},
	}

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"resolves from options", "${DOCKER_PATH}", "podman"},
		{"literal passthrough", "docker", "docker"},
		{"empty option falls back to os env", "${EMPTY}", ""},
		{"unknown key falls back to os env", "${DEVSY_TEST_OSVAR}", "from-os"},
		{"missing everywhere is empty", "${DEVSY_TEST_ABSENT}", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := expandWithOptions(tc.in, opts); got != tc.want {
				t.Fatalf("expandWithOptions(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestExecOneShotOptions_ResolveTimeout_Clamp(t *testing.T) {
	opts := ExecOneShotOptions{
		TimeoutSeconds:    10000,
		TimeoutSecondsMax: 60,
	}
	clamped, wasClamped := opts.ResolveTimeout(5)
	if !wasClamped {
		t.Fatal("expected clamp=true")
	}
	if clamped.Seconds() != 60 {
		t.Fatalf("expected 60s, got %s", clamped)
	}
}

func TestExecOneShotOptions_ResolveTimeout_Default(t *testing.T) {
	opts := ExecOneShotOptions{TimeoutSecondsMax: 600}
	clamped, wasClamped := opts.ResolveTimeout(300)
	if wasClamped {
		t.Fatal("expected clamp=false")
	}
	if clamped.Seconds() != 300 {
		t.Fatalf("expected 300s, got %s", clamped)
	}
}

func TestExecOneShotOptions_ResolveTimeout_CallerExplicit(t *testing.T) {
	opts := ExecOneShotOptions{
		TimeoutSeconds:    120,
		TimeoutSecondsMax: 600,
	}
	clamped, wasClamped := opts.ResolveTimeout(300)
	if wasClamped {
		t.Fatal("expected clamp=false")
	}
	if clamped.Seconds() != 120 {
		t.Fatalf("expected 120s, got %s", clamped)
	}
}

// fakeRuntime is a test double for ContainerRuntime.
type fakeRuntime struct {
	findResult *devcconfig.ContainerDetails
	findErr    error
	execExit   int
	execErr    error
	execStdout string
	execStderr string
	probeEnv   map[string]string
}

func (f *fakeRuntime) FindRunning(
	_ context.Context,
	_ string,
	_ []string,
) (*devcconfig.ContainerDetails, error) {
	return f.findResult, f.findErr
}

func (f *fakeRuntime) Command() string { return "fake" }

func (f *fakeRuntime) Environment() []string { return nil }

func (f *fakeRuntime) Exec(_ context.Context, req ExecRequest) (int, error) {
	stdout := req.Stdout
	if stdout == nil {
		stdout = io.Discard
	}
	stderr := req.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	if f.execStdout != "" {
		_, _ = stdout.Write([]byte(f.execStdout))
	}
	if f.execStderr != "" {
		_, _ = stderr.Write([]byte(f.execStderr))
	}
	return f.execExit, f.execErr
}

func (f *fakeRuntime) ProbeEnv(
	_ context.Context,
	_ ContainerTarget,
	_ string,
) map[string]string {
	return f.probeEnv
}

func TestExecOneShot_ExitCodeAndOutput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	runtime := &fakeRuntime{
		execExit:   42,
		execStdout: "hi",
	}

	exitCode, err := execOneShotWithRuntime(
		context.Background(),
		runtime,
		ExecRequest{
			Target:  ContainerTarget{ContainerID: "ctr1"},
			Workdir: "/workdir",
			Argv:    []string{"echo", "hi"},
			Stdout:  &stdout,
			Stderr:  &stderr,
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exitCode != 42 {
		t.Fatalf("expected exit code 42, got %d", exitCode)
	}
	if stdout.String() != "hi" {
		t.Fatalf("expected stdout %q, got %q", "hi", stdout.String())
	}
}

func TestExecOneShot_PartialOutputOnError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	runtime := &fakeRuntime{
		execExit:   -1,
		execErr:    context.Canceled,
		execStdout: "partial",
	}

	_, err := execOneShotWithRuntime(
		context.Background(),
		runtime,
		ExecRequest{
			Target:  ContainerTarget{ContainerID: "ctr2"},
			Workdir: "/workdir",
			Argv:    []string{"long-running-cmd"},
			Stdout:  &stdout,
			Stderr:  &stderr,
		},
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if stdout.String() != "partial" {
		t.Fatalf("expected partial stdout %q to be preserved, got %q", "partial", stdout.String())
	}
}
