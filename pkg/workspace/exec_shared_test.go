//go:build !windows

package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"testing"
)

// TestExecWithRunner covers the shared exec path used by both the Docker and
// Apple runtimes: argument assembly and exit-code translation.
func TestExecWithRunner(t *testing.T) {
	var gotArgs []string
	run := func(_ context.Context, args []string, _ io.Reader, _, _ io.Writer) error {
		gotArgs = args
		return nil
	}

	req := ExecRequest{
		Target:  ContainerTarget{ContainerID: "cid", User: "vscode"},
		Workdir: "/work",
		Env:     map[string]string{"WSVAR": "wsval"},
		Argv:    []string{"echo", "hi"},
	}
	code, err := execWithRunner(context.Background(), req, run)
	if err != nil || code != 0 {
		t.Fatalf("execWithRunner = (%d, %v), want (0, nil)", code, err)
	}
	joined := strings.Join(gotArgs, " ")
	for _, want := range []string{"exec -i", "-e WSVAR=wsval", "--workdir /work", "--user vscode", "cid", "echo hi"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q: %s", want, joined)
		}
	}
}

func TestExecWithRunnerExitCode(t *testing.T) {
	// A non-zero process exit is reported as an exit code, not a machinery error.
	exitRun := func(_ context.Context, _ []string, _ io.Reader, _, _ io.Writer) error {
		return exitErr(t, 7)
	}
	target := ExecRequest{Target: ContainerTarget{ContainerID: "c"}}
	code, err := execWithRunner(context.Background(), target, exitRun)
	if err != nil {
		t.Fatalf("exit error must not surface as machinery error: %v", err)
	}
	if code != 7 {
		t.Errorf("exit code = %d, want 7", code)
	}

	// A genuine machinery failure surfaces as (-1, err).
	failRun := func(_ context.Context, _ []string, _ io.Reader, _, _ io.Writer) error {
		return errors.New("binary not found")
	}
	req := ExecRequest{Target: ContainerTarget{ContainerID: "c"}}
	code, err = execWithRunner(context.Background(), req, failRun)
	if err == nil || code != -1 {
		t.Errorf("machinery failure = (%d, %v), want (-1, err)", code, err)
	}
}

func TestProbeEnvWithRunner(t *testing.T) {
	// /proc/self/environ succeeds → NUL-separated parse.
	run := func(_ context.Context, _ []string, _ io.Reader, stdout, _ io.Writer) error {
		_, _ = stdout.Write([]byte("PATH=/bin\x00HOME=/root\x00"))
		return nil
	}
	target := ContainerTarget{ContainerID: "c"}
	env := probeEnvWithRunner(context.Background(), target, "loginInteractiveShell", run)
	if env["PATH"] != "/bin" || env["HOME"] != "/root" {
		t.Errorf("probed env = %v", env)
	}

	// Total failure → empty map (documented contract), never a panic.
	failRun := func(_ context.Context, _ []string, _ io.Reader, _, _ io.Writer) error {
		return errors.New("exec failed")
	}
	got := probeEnvWithRunner(context.Background(), target, "loginInteractiveShell", failRun)
	if len(got) != 0 {
		t.Errorf("expected empty map on failure, got %v", got)
	}
}

// exitErr returns a real *exec.ExitError carrying the given exit code, produced
// by running `sh -c "exit <code>"`.
func exitErr(t *testing.T, code int) error {
	t.Helper()
	//nolint:gosec // G204: test-controlled constant exit code, not user input
	err := exec.Command("sh", "-c", fmt.Sprintf("exit %d", code)).Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("could not synthesize exit error: %v", err)
	}
	return ee
}
