//go:build !windows

package workspace

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"al.essio.dev/pkg/shellescape"
	"github.com/creack/pty"
	workspace2 "github.com/devsy-org/devsy/pkg/workspace"
	"github.com/stretchr/testify/require"
	"golang.org/x/term"
)

type execTTYCaptureRuntime struct {
	workspace2.ContainerRuntime
	request workspace2.ExecRequest
}

func (r *execTTYCaptureRuntime) Exec(
	_ context.Context,
	request workspace2.ExecRequest,
) (int, error) {
	r.request = request
	return 0, nil
}

func TestExecInContainerRequestsTTYOnlyWhenInputAndOutputAreTerminals(t *testing.T) {
	tests := []struct {
		name      string
		stdinTTY  bool
		stdoutTTY bool
		wantTTY   bool
	}{
		{name: "both terminals", stdinTTY: true, stdoutTTY: true, wantTTY: true},
		{name: "terminal input with redirected output", stdinTTY: true},
		{name: "redirected input with terminal output", stdoutTTY: true},
		{name: "neither terminal"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stdin, stdout := openExecTTYDescriptors(t, test.stdinTTY, test.stdoutTTY)
			require.Equal(t, test.stdinTTY, isExecTTY(stdin))
			require.Equal(t, test.stdoutTTY, isExecTTY(stdout))

			runtime := &execTTYCaptureRuntime{}
			target := workspace2.ContainerTarget{ContainerID: "container-id", User: "1001:1002"}
			env := map[string]string{"LANG": "C.UTF-8"}
			originalStdin, originalStdout, originalStderr := os.Stdin, os.Stdout, os.Stderr
			os.Stdin, os.Stdout, os.Stderr = stdin, stdout, stdin
			t.Cleanup(func() {
				os.Stdin, os.Stdout, os.Stderr = originalStdin, originalStdout, originalStderr
			})
			err := (&ExecCmd{}).execInContainer(
				t.Context(),
				execOpts{runtime: runtime, target: target, workdir: "/workspace", envMap: env},
				[]string{"printf", "hello"},
			)
			os.Stdin, os.Stdout, os.Stderr = originalStdin, originalStdout, originalStderr
			require.NoError(t, err)
			require.Equal(t, test.wantTTY, runtime.request.TTY)
			require.Equal(t, test.stdoutTTY, runtime.request.LiveStdout)
			require.Equal(t, test.stdinTTY, runtime.request.LiveStderr)
			require.Equal(t, target, runtime.request.Target)
			require.Equal(t, "/workspace", runtime.request.Workdir)
			require.Equal(t, env, runtime.request.Env)
			require.Equal(t, []string{"printf", "hello"}, runtime.request.Argv)
			require.Same(t, stdin, runtime.request.Stdin)
			require.Same(t, stdout, runtime.request.Stdout)
			require.Same(t, stdin, runtime.request.Stderr)
		})
	}
}

func openExecTTYDescriptors(t *testing.T, stdinTTY, stdoutTTY bool) (*os.File, *os.File) {
	t.Helper()

	var master, slave *os.File
	if stdinTTY || stdoutTTY {
		var err error
		master, slave, err = pty.Open()
		require.NoError(t, err)
		t.Cleanup(func() {
			_ = master.Close()
			_ = slave.Close()
		})
	}

	stdin := master
	if !stdinTTY {
		stdin = createExecRegularFile(t)
	}

	stdout := slave
	if !stdoutTTY {
		stdout = createExecRegularFile(t)
	}

	return stdin, stdout
}

func createExecRegularFile(t *testing.T) *os.File {
	t.Helper()

	file, err := os.CreateTemp(t.TempDir(), "exec-descriptor-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = file.Close() })
	return file
}

func isExecTTY(file *os.File) bool {
	return term.IsTerminal(
		int(file.Fd()),
	) // #nosec G115 -- descriptor comes from an open test file.
}

func TestExecInContainerPreservesPipedChildBytesAndExit(t *testing.T) {
	const secret = "raw-exec-secret-92815"
	const stdoutPayload = "{\"kind\":\"status\",\"state\":\"failed\"}\x00" + secret + "\xfftail"
	const stderrPayload = "{\"kind\":\"error\",\"message\":\"child\"}\x00" + secret + "\xfetail"
	runRawExecHelper(stdoutPayload, stderrPayload)

	t.Setenv("DEVSY_RAW_EXEC_HELPER", "1")
	t.Setenv("DEVSY_RAW_EXEC_SECRET", secret)
	wrapper := rawExecWrapper(t)
	stdin, err := os.Open(os.DevNull)
	require.NoError(t, err)
	defer func() { _ = stdin.Close() }()
	stdoutRead, stdoutWrite, err := os.Pipe()
	require.NoError(t, err)
	defer func() { _ = stdoutRead.Close() }()
	defer func() { _ = stdoutWrite.Close() }()
	stderrRead, stderrWrite, err := os.Pipe()
	require.NoError(t, err)
	defer func() { _ = stderrRead.Close() }()
	defer func() { _ = stderrWrite.Close() }()
	originalStdin, originalStdout, originalStderr := os.Stdin, os.Stdout, os.Stderr
	os.Stdin, os.Stdout, os.Stderr = stdin, stdoutWrite, stderrWrite
	t.Cleanup(
		func() { os.Stdin, os.Stdout, os.Stderr = originalStdin, originalStdout, originalStderr },
	)

	err = (&ExecCmd{}).execInContainer(t.Context(), execOpts{
		runtime: workspace2.NewDockerRuntime(nil, wrapper),
		target:  workspace2.ContainerTarget{ContainerID: sourceContainer},
		envMap:  map[string]string{"TOKEN": secret},
	}, []string{"ignored"})
	os.Stdin, os.Stdout, os.Stderr = originalStdin, originalStdout, originalStderr
	require.NoError(t, stdoutWrite.Close())
	require.NoError(t, stderrWrite.Close())
	var exitErr *workspace2.CommandExitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 23, exitErr.Code)
	gotStdout, err := io.ReadAll(stdoutRead)
	require.NoError(t, err)
	gotStderr, err := io.ReadAll(stderrRead)
	require.NoError(t, err)
	require.Equal(t, []byte(stdoutPayload), gotStdout)
	require.Equal(t, []byte(stderrPayload), gotStderr)
}

func runRawExecHelper(stdoutPayload, stderrPayload string) {
	if os.Getenv("DEVSY_RAW_EXEC_HELPER") != "1" {
		return
	}
	if _, err := os.Stdout.Write([]byte(stdoutPayload)); err != nil {
		os.Exit(97)
	}
	if _, err := os.Stderr.Write([]byte(stderrPayload)); err != nil {
		os.Exit(98)
	}
	os.Exit(23)
}

func rawExecWrapper(t *testing.T) string {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	wrapper := filepath.Join(t.TempDir(), "docker-helper")
	script := "#!/bin/sh\nexec " + shellescape.Quote(executable) +
		" -test.run=^TestExecInContainerPreservesPipedChildBytesAndExit$ -- \"$@\"\n"
	require.NoError(t, os.WriteFile(wrapper, []byte(script), 0o700))
	return wrapper
}
