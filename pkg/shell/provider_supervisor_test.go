package shell

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/interp"
)

var providerTestAckToken = strings.Repeat("0123456789abcdef", 4)

func TestProviderShellProcess(t *testing.T) {
	if os.Getenv("DEVSY_PROVIDER_SHELL_PROCESS") != "1" {
		return
	}
	err := RunProviderShell(context.Background(), os.Getenv("DEVSY_PROVIDER_SCRIPT"),
		os.Getenv("DEVSY_PROVIDER_FORCE"), os.Getenv("DEVSY_PROVIDER_ACK"))
	if err == nil {
		os.Exit(0)
	}
	if status, ok := errors.AsType[interp.ExitStatus](err); ok {
		os.Exit(int(status))
	}
	if errors.Is(err, context.Canceled) {
		os.Exit(0)
	}
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func TestProviderShellRawOutput(t *testing.T) {
	if os.Getenv("DEVSY_PROVIDER_SHELL_RAW") != "1" {
		return
	}
	_, _ = os.Stdout.Write([]byte{0xff, 0, '\n'})
	_, _ = os.Stderr.Write([]byte{0, 0xfe})
	os.Exit(0)
}

func TestProviderShellTrackedChild(t *testing.T) {
	if os.Getenv("DEVSY_PROVIDER_SHELL_CHILD") != "1" {
		return
	}
	if err := writeTrackedChildPID(os.Getenv("DEVSY_PROVIDER_CHILD_PID")); err != nil {
		os.Exit(1)
	}
	_, _ = fmt.Fprintln(os.Stdout, "ready")
	time.Sleep(time.Minute)
	os.Exit(0)
}

func TestProviderShellTokenChild(t *testing.T) {
	if os.Getenv("DEVSY_PROVIDER_SHELL_TOKEN_CHILD") != "1" {
		return
	}
	if !providerTokenChildClean() {
		_, _ = fmt.Fprint(os.Stdout, "leaked")
		os.Exit(1)
	}
	_, _ = fmt.Fprint(os.Stdout, "clean")
	os.Exit(0)
}

func providerTokenChildClean() bool {
	if strings.Contains(strings.Join(os.Environ(), "\x00"), providerTestAckToken) ||
		!providerProcEnvironClean() || !providerAckKeyAbsent() {
		return false
	}
	for _, arg := range os.Args {
		if strings.Contains(arg, providerTestAckToken) {
			return false
		}
	}
	return true
}

func providerProcEnvironClean() bool {
	if runtime.GOOS != "linux" {
		return true
	}
	root, err := os.OpenRoot("/proc")
	if err != nil {
		return false
	}
	defer func() { _ = root.Close() }()
	for _, path := range []string{"self/environ", strconv.Itoa(os.Getppid()) + "/environ"} {
		environ, err := root.ReadFile(path)
		if err != nil || bytes.Contains(environ, []byte(providerTestAckToken)) {
			return false
		}
	}
	return true
}

func providerAckKeyAbsent() bool {
	root, err := os.OpenRoot(filepath.Dir(os.Getenv("DEVSY_PROVIDER_ACK")))
	if err != nil {
		return false
	}
	defer func() { _ = root.Close() }()
	_, err = root.Lstat("ack-key")
	return errors.Is(err, os.ErrNotExist)
}

func writeTrackedChildPID(path string) error {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	file, err := root.OpenFile("pid", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errors.Join(err, root.Close())
	}
	_, writeErr := file.WriteString(strconv.Itoa(os.Getpid()))
	return errors.Join(writeErr, file.Close(), root.Close())
}

func providerShellFixture(t *testing.T, script string) (string, string, string) {
	t.Helper()
	dir := providerPrivateTestDir(t)
	scriptPath := filepath.Join(dir, "script")
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o600))
	return scriptPath, filepath.Join(dir, "force"), filepath.Join(dir, "ack")
}

func providerTestWriteKey(t *testing.T, ackPath, token string) {
	t.Helper()
	require.NoError(
		t,
		os.WriteFile(filepath.Join(filepath.Dir(ackPath), "ack-key"), []byte(token), 0o600),
	)
}

func providerShellCommand(t *testing.T, scriptPath, forcePath, ackPath string) *exec.Cmd {
	t.Helper()
	providerTestWriteKey(t, ackPath, providerTestAckToken)
	binary, err := os.Executable()
	require.NoError(t, err)
	cmd := &exec.Cmd{Path: binary, Args: []string{binary, "-test.run=^TestProviderShellProcess$"}}
	cmd.Env = append(os.Environ(), "DEVSY_PROVIDER_SHELL_PROCESS=1",
		"DEVSY_PROVIDER_SCRIPT="+scriptPath, "DEVSY_PROVIDER_FORCE="+forcePath,
		"DEVSY_PROVIDER_ACK="+ackPath)
	return cmd
}

func runProviderShellWithToken(t *testing.T, scriptPath, forcePath, ackPath string) error {
	t.Helper()
	providerTestWriteKey(t, filepath.Join(filepath.Dir(scriptPath), "ack"), providerTestAckToken)
	return RunProviderShell(context.Background(), scriptPath, forcePath, ackPath)
}

func providerTestReadAck(ackPath string) ([]byte, error) {
	root, err := os.OpenRoot(filepath.Dir(ackPath))
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return root.ReadFile("ack")
}

func TestRunProviderShellRejectsInvalidAckTokens(t *testing.T) {
	for _, test := range []struct {
		name  string
		token string
	}{
		{name: "missing"},
		{name: "empty", token: ""},
		{name: "short", token: strings.Repeat("a", 63)},
		{name: "long", token: strings.Repeat("a", 65)},
		{name: "newline", token: providerTestAckToken + "\n"},
		{name: "uppercase", token: strings.Repeat("A", 64)},
		{name: "nonhex", token: strings.Repeat("g", 64)},
	} {
		t.Run(test.name, func(t *testing.T) {
			scriptPath, forcePath, ackPath := providerShellFixture(t, "printf should-not-run")
			if test.name != "missing" {
				providerTestWriteKey(t, ackPath, test.token)
			}
			err := RunProviderShell(context.Background(), scriptPath, forcePath, ackPath)
			require.ErrorContains(t, err, "invalid provider shell acknowledgement token")
			_, err = os.Lstat(ackPath)
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestRunProviderShellRejectsUnsafeAckKey(t *testing.T) {
	t.Run("directory", func(t *testing.T) {
		scriptPath, forcePath, ackPath := providerShellFixture(t, "printf should-not-run")
		require.NoError(t, os.Mkdir(filepath.Join(filepath.Dir(ackPath), "ack-key"), 0o700))
		require.ErrorContains(
			t,
			RunProviderShell(context.Background(), scriptPath, forcePath, ackPath),
			"invalid provider shell acknowledgement token",
		)
		_, err := os.Lstat(ackPath)
		require.ErrorIs(t, err, os.ErrNotExist)
	})
	if runtime.GOOS == providerWindowsOS {
		return
	}
	t.Run("symlink", func(t *testing.T) {
		scriptPath, forcePath, ackPath := providerShellFixture(t, "printf should-not-run")
		require.NoError(t, os.Symlink(scriptPath, filepath.Join(filepath.Dir(ackPath), "ack-key")))
		require.ErrorContains(
			t,
			RunProviderShell(context.Background(), scriptPath, forcePath, ackPath),
			"invalid provider shell acknowledgement token",
		)
		_, err := os.Lstat(ackPath)
		require.ErrorIs(t, err, os.ErrNotExist)
	})
	t.Run("permissions", func(t *testing.T) {
		scriptPath, forcePath, ackPath := providerShellFixture(t, "printf should-not-run")
		providerTestWriteKey(t, ackPath, providerTestAckToken)
		require.NoError(t, os.Chmod(filepath.Join(filepath.Dir(ackPath), "ack-key"), 0o400))
		require.ErrorContains(
			t,
			RunProviderShell(context.Background(), scriptPath, forcePath, ackPath),
			"invalid provider shell acknowledgement token",
		)
		_, err := os.Lstat(ackPath)
		require.ErrorIs(t, err, os.ErrNotExist)
	})
}

func TestRunProviderShellDoesNotLeakAckToken(t *testing.T) {
	binary, err := os.Executable()
	require.NoError(t, err)
	command := strconv.Quote(binary) + " -test.run=^TestProviderShellTokenChild$"
	if runtime.GOOS == providerWindowsOS {
		command = strings.ReplaceAll(command, `\`, `/`)
	}
	scriptPath, forcePath, ackPath := providerShellFixture(t, command)
	cmd := providerShellCommand(t, scriptPath, forcePath, ackPath)
	cmd.Env = append(cmd.Env, "DEVSY_PROVIDER_SHELL_TOKEN_CHILD=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	require.NoError(t, cmd.Run())
	require.Equal(t, "clean", stdout.String())
	require.NotContains(t, stderr.String(), providerTestAckToken)
	ack, err := providerTestReadAck(ackPath)
	require.NoError(t, err)
	require.Equal(t, providerTestAckToken, string(ack))
}

func TestRunProviderShellRejectsForgedAck(t *testing.T) {
	for _, forged := range []string{"", strings.Repeat("f", 64)} {
		t.Run(strconv.Itoa(len(forged)), func(t *testing.T) {
			scriptPath, forcePath, ackPath := providerShellFixture(t, "exit 0")
			require.NoError(t, os.WriteFile(ackPath, []byte(forged), 0o600))
			cmd := providerShellCommand(t, scriptPath, forcePath, ackPath)
			output, err := cmd.CombinedOutput()
			require.Error(t, err)
			require.NotContains(t, string(output), providerTestAckToken)
			ack, err := providerTestReadAck(ackPath)
			require.NoError(t, err)
			require.Equal(t, forged, string(ack))
		})
	}
}

func TestRunProviderShellNormalExitStatuses(t *testing.T) {
	for _, test := range []struct {
		script string
		status int
	}{
		{"printf normal", 0},
		{"exit 7", 7},
		{"exit 125", 125},
		{"command-that-does-not-exist-846307", 127},
	} {
		t.Run(strconv.Itoa(test.status), func(t *testing.T) {
			scriptPath, forcePath, ackPath := providerShellFixture(t, test.script)
			cmd := providerShellCommand(t, scriptPath, forcePath, ackPath)
			output, err := cmd.CombinedOutput()
			if test.status == 0 {
				require.NoError(t, err)
				require.Equal(t, "normal", string(output))
			} else {
				exit, ok := errors.AsType[*exec.ExitError](err)
				require.True(t, ok)
				require.Equal(t, test.status, exit.ExitCode())
			}
			ack, err := providerTestReadAck(ackPath)
			require.NoError(t, err)
			require.Equal(t, providerTestAckToken, string(ack))
		})
	}
}

func TestRunProviderShellRawStreams(t *testing.T) {
	binary, err := os.Executable()
	require.NoError(t, err)
	command := strconv.Quote(binary) + " -test.run=^TestProviderShellRawOutput$"
	if runtime.GOOS == providerWindowsOS {
		command = strings.ReplaceAll(command, `\`, `/`)
	}
	scriptPath, forcePath, ackPath := providerShellFixture(t, command)
	cmd := providerShellCommand(t, scriptPath, forcePath, ackPath)
	cmd.Env = append(cmd.Env, "DEVSY_PROVIDER_SHELL_RAW=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	require.NoError(t, cmd.Run())
	require.Equal(t, []byte{0xff, 0, '\n'}, stdout.Bytes())
	require.Equal(t, []byte{0, 0xfe}, stderr.Bytes())
}

func TestRunProviderShellForceAcknowledgesReapedChild(t *testing.T) {
	binary, err := os.Executable()
	require.NoError(t, err)
	command := strconv.Quote(binary) + " -test.run=^TestProviderShellTrackedChild$"
	if runtime.GOOS == providerWindowsOS {
		command = strings.ReplaceAll(command, `\`, `/`)
	}
	scriptPath, forcePath, ackPath := providerShellFixture(t, command+"; printf finished")
	cmd := providerShellCommand(t, scriptPath, forcePath, ackPath)
	pidPath := filepath.Join(filepath.Dir(scriptPath), "pid")
	cmd.Env = append(cmd.Env, "DEVSY_PROVIDER_SHELL_CHILD=1", "DEVSY_PROVIDER_CHILD_PID="+pidPath)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	ready := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		ready <- line
	}()
	select {
	case line := <-ready:
		require.Equal(t, "ready\n", line)
	case <-time.After(5 * time.Second):
		t.Fatal("provider child did not start")
	}
	require.NoError(t, os.WriteFile(forcePath, nil, 0o600))
	require.Eventually(t, func() bool {
		content, err := providerTestReadAck(ackPath)
		return err == nil && string(content) == providerTestAckToken
	}, 5*time.Second, 20*time.Millisecond)
	root, err := os.OpenRoot(filepath.Dir(scriptPath))
	require.NoError(t, err)
	defer func() { _ = root.Close() }()
	pidBytes, err := root.ReadFile("pid")
	require.NoError(t, err)
	pid, err := strconv.Atoi(string(pidBytes))
	require.NoError(t, err)
	require.NoError(t, providerChildReaped(pid))
	require.NoError(t, cmd.Wait())
	require.NotContains(t, stderr.String(), command)
}

func TestRunProviderShellBlockedBuiltinAcknowledges(t *testing.T) {
	scriptPath, forcePath, ackPath := providerShellFixture(
		t,
		"printf 'ready\\n'; mapfile lines; printf finished",
	)
	cmd := providerShellCommand(t, scriptPath, forcePath, ackPath)
	stdinRead, stdinWrite, err := os.Pipe()
	require.NoError(t, err)
	defer func() { _ = stdinRead.Close() }()
	defer func() { _ = stdinWrite.Close() }()
	cmd.Stdin = stdinRead
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	ready := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		ready <- line
	}()
	select {
	case line := <-ready:
		require.Equal(t, "ready\n", line)
	case <-time.After(5 * time.Second):
		t.Fatal("provider shell did not start")
	}
	require.NoError(t, os.WriteFile(forcePath, nil, 0o600))
	require.Eventually(t, func() bool {
		content, err := providerTestReadAck(ackPath)
		return err == nil && string(content) == providerTestAckToken
	}, 5*time.Second, 20*time.Millisecond)
	require.NoError(t, cmd.Process.Kill())
	_ = cmd.Wait()
	require.NotContains(t, stderr.String(), "finished")
}

func TestProviderProcessSupervisorStartForceRace(t *testing.T) {
	binary, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	supervisor := &providerProcessSupervisor{}
	ready := make(chan struct{})
	var readyOnce sync.Once
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			cmd, startErr := supervisor.start(ctx, func() *exec.Cmd {
				readyOnce.Do(func() { close(ready) })
				child := &exec.Cmd{
					Path: binary,
					Args: []string{binary, "-test.run=^TestProviderExecHelper$"},
				}
				child.Env = append(
					os.Environ(),
					"DEVSY_PROVIDER_EXEC_HELPER=1",
					"DEVSY_PROVIDER_EXEC_EXIT=wait",
				)
				child.Stdout = io.Discard
				return child
			})
			if startErr == nil {
				_ = cmd.Wait()
				supervisor.reaped(cmd)
			}
		})
	}
	<-ready
	supervisor.force(cancel)
	workers.Wait()
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	require.True(t, supervisor.forced)
	require.Empty(t, supervisor.children)
}

func TestRunProviderShellPreexistingForceIsAcknowledged(t *testing.T) {
	scriptPath, forcePath, ackPath := providerShellFixture(t, "exit 7")
	require.NoError(t, os.WriteFile(forcePath, nil, 0o600))
	cmd := providerShellCommand(t, scriptPath, forcePath, ackPath)
	output, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "provider shell: %s", string(output))
	info, err := os.Lstat(ackPath)
	require.NoError(t, err)
	require.True(t, info.Mode().IsRegular())
	ack, err := providerTestReadAck(ackPath)
	require.NoError(t, err)
	require.Equal(t, providerTestAckToken, string(ack))
}

func TestRunProviderShellRejectsUnsafePaths(t *testing.T) {
	scriptPath, forcePath, ackPath := providerShellFixture(t, "exit 0")
	require.Error(t, runProviderShellWithToken(t, scriptPath, forcePath, scriptPath))
	require.Error(
		t,
		runProviderShellWithToken(
			t,
			scriptPath,
			forcePath,
			filepath.Join(t.TempDir(), "ack"),
		),
	)
	if runtime.GOOS == providerWindowsOS {
		require.NoError(t, os.Mkdir(ackPath, 0o700))
	} else {
		require.NoError(t, os.Symlink(scriptPath, ackPath))
	}
	require.Error(t, runProviderShellWithToken(t, scriptPath, forcePath, ackPath))
}
