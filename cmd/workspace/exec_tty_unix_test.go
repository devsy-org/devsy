//go:build !windows

package workspace

import (
	"context"
	"os"
	"testing"

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
			originalStdin, originalStdout := os.Stdin, os.Stdout
			os.Stdin, os.Stdout = stdin, stdout
			t.Cleanup(func() {
				os.Stdin, os.Stdout = originalStdin, originalStdout
			})
			err := (&ExecCmd{}).execInContainer(
				t.Context(),
				execOpts{runtime: runtime, target: target, workdir: "/workspace", envMap: env},
				[]string{"printf", "hello"},
			)
			os.Stdin, os.Stdout = originalStdin, originalStdout
			require.NoError(t, err)
			require.Equal(t, test.wantTTY, runtime.request.TTY)
			require.Equal(t, target, runtime.request.Target)
			require.Equal(t, "/workspace", runtime.request.Workdir)
			require.Equal(t, env, runtime.request.Env)
			require.Equal(t, []string{"printf", "hello"}, runtime.request.Argv)
			require.Same(t, stdin, runtime.request.Stdin)
			require.Same(t, stdout, runtime.request.Stdout)
			require.Same(t, os.Stderr, runtime.request.Stderr)
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
