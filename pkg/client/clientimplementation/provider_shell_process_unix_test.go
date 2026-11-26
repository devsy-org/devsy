//go:build !windows

package clientimplementation

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/interp"
)

func TestProviderShellProcessIsolation(t *testing.T) {
	dir, err := createProviderInvocationDir()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	files, err := prepareProviderShell(dir, "printf 'ready\\n'; read value")
	require.NoError(t, err)
	binary, err := os.Executable()
	require.NoError(t, err)
	cmd := &exec.Cmd{
		Path: binary,
		Args: []string{
			binary, "internal", "provider-shell",
			"--script-file", files.script, "--force-file", files.force, "--ack-file", files.ack,
			"--parent-pid", strconv.Itoa(os.Getpid()),
		},
	}
	input, err := cmd.StdinPipe()
	require.NoError(t, err)
	defer func() { _ = input.Close() }()
	output, err := cmd.StdoutPipe()
	require.NoError(t, err)
	configureProviderShellProcess(cmd)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	ready := make(chan bool, 1)
	go func() { ready <- bufio.NewScanner(output).Scan() }()
	select {
	case ok := <-ready:
		require.True(t, ok)
	case <-time.After(5 * time.Second):
		t.Fatal("helper did not become ready")
	}
	group, err := syscall.Getpgid(cmd.Process.Pid)
	require.NoError(t, err)
	require.Equal(t, syscall.Getpgrp(), group)
	require.NoError(t, cmd.Process.Signal(syscall.SIGINT))
	require.NoError(t, publishProviderMarker(files.force))
	require.Eventually(t, func() bool {
		ack, err := os.ReadFile(files.ack)
		return err == nil && string(ack) == files.ackToken
	}, 5*time.Second, 20*time.Millisecond)
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
}

func TestProviderShellPTYController(t *testing.T) {
	if os.Getenv("DEVSY_PROVIDER_PTY_CONTROLLER") != "1" {
		return
	}
	signal.Ignore(syscall.SIGHUP)
	defer signal.Reset(syscall.SIGHUP)
	err := RunCommand(context.Background(), RunCommandOptions{
		Command:      types.StrArray{`read value; printf 'got:%s\n' "$value"`},
		Environ:      os.Environ(),
		Stdin:        os.Stdin,
		Stdout:       os.Stdout,
		Stderr:       os.Stderr,
		RawStdout:    true,
		Cancellation: provider.ProviderCancellationFile,
	})
	require.NoError(t, err)
}

func TestProviderShellReadsControllingTerminal(t *testing.T) {
	master, slave, err := pty.Open()
	require.NoError(t, err)
	defer func() { _ = master.Close() }()
	defer func() { _ = slave.Close() }()
	binary, err := os.Executable()
	require.NoError(t, err)
	controller := &exec.Cmd{
		Path:  binary,
		Args:  []string{binary, "-test.run=^TestProviderShellPTYController$"},
		Env:   append(os.Environ(), "DEVSY_PROVIDER_PTY_CONTROLLER=1"),
		Stdin: slave, Stderr: os.Stderr,
		SysProcAttr: &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0},
	}
	output, err := controller.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, controller.Start())
	t.Cleanup(func() { _ = controller.Process.Kill() })
	_, err = master.Write([]byte("hello\n"))
	require.NoError(t, err)
	lines := make(chan []string, 1)
	go func() {
		reader := bufio.NewReader(output)
		first, _ := reader.ReadString('\n')
		second, _ := reader.ReadString('\n')
		lines <- []string{first, second}
	}()
	select {
	case got := <-lines:
		require.Equal(t, []string{"got:hello\n", "PASS\n"}, got)
	case <-time.After(8 * time.Second):
		t.Fatal("provider helper could not read the controlling terminal")
	}
	require.NoError(t, master.Close())
	require.NoError(t, slave.Close())
	done := make(chan error, 1)
	go func() { done <- controller.Wait() }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("provider controller did not finish after terminal closure")
	}
}

func TestProviderShellForegroundPreservesChildSIGINT(t *testing.T) {
	if signal.Ignored(syscall.SIGINT) {
		t.Skip("parent already ignores SIGINT")
	}
	var stdout bytes.Buffer
	err := RunCommand(context.Background(), RunCommandOptions{
		Command:      types.StrArray{`/bin/sh -c 'kill -INT $$; printf survived'`},
		Environ:      os.Environ(),
		Stdout:       &stdout,
		RawStdout:    true,
		Cancellation: provider.ProviderCancellationFile,
	})
	var status interp.ExitStatus
	require.ErrorAs(t, err, &status)
	require.Equal(t, 130, int(status))
	require.NotContains(t, stdout.String(), "survived")
}
