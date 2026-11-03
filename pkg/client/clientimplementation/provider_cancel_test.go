package clientimplementation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/shell"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/interp"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 11 && os.Args[1] == "internal" && os.Args[2] == "provider-shell" {
		os.Exit(runProviderShellTestHelper(os.Args))
	}
	os.Exit(m.Run())
}

func runProviderShellTestHelper(args []string) int {
	if !validProviderShellTestArgs(args) {
		return 1
	}
	parentPID, err := strconv.Atoi(args[10])
	if err != nil {
		return 1
	}
	cleanup, err := shell.PrepareProviderShellForeground(parentPID)
	if err != nil {
		return 1
	}
	defer cleanup()
	err = shell.RunProviderShell(context.Background(), shell.ProviderShellInvocation{
		ScriptPath: args[4], ForcePath: args[6], AckPath: args[8], ParentPID: parentPID,
	})
	if err == nil {
		return 0
	}
	if status, ok := errors.AsType[interp.ExitStatus](err); ok {
		return int(status)
	}
	return 1
}

func validProviderShellTestArgs(args []string) bool {
	return args[3] == "--script-file" && args[5] == "--force-file" &&
		args[7] == "--ack-file" && args[9] == "--parent-pid"
}

const (
	testTransportRead         = "transport-read"
	testWindowsOS             = "windows"
	testCancelProvider        = "cancel-provider"
	testOpaqueCommand         = "opaque"
	testCancellationHelperArg = "-test.run=^TestProviderCancellationHelper$"
)

func TestProviderCancellationHelper(t *testing.T) {
	if os.Getenv("DEVSY_CANCEL_HELPER") != "1" {
		return
	}
	if os.Getenv("DEVSY_CANCEL_HOLDER") == "1" {
		time.Sleep(time.Minute)
		return
	}
	runTestProviderCancellationBehavior(os.Getenv(config.EnvProviderCancelFile))
	os.Exit(0)
}

func runTestProviderCancellationBehavior(marker string) {
	switch behavior := os.Getenv("DEVSY_CANCEL_BEHAVIOR"); behavior {
	case "acknowledge":
		runAcknowledgingCancellationHelper(marker)
	case "ignore":
		runIgnoringCancellationHelper()
	case testTransportRead, "transport-write":
		testTransportCancellationHelper(behavior, marker)
	case "bulk-after-eof":
		writeTestCancellationBulkAfterEOF()
	case "orphan-output":
		startTestCancellationOutputHolder()
	case "orphan-input":
		startTestCancellationInputHolder()
	default:
		runTestCancellationExit(behavior, marker)
	}
}

func runAcknowledgingCancellationHelper(marker string) {
	if _, err := fmt.Fprint(os.Stdout, "ready"); err != nil {
		os.Exit(126)
	}
	awaitTestCancellationMarker(marker, os.Getenv("DEVSY_CANCEL_ACK_PATH"))
}

func runIgnoringCancellationHelper() {
	if path := os.Getenv("DEVSY_CANCEL_CHILD_PID_PATH"); path != "" {
		exitTestHelperOnError(testHelperWriteFile(path, strconv.Itoa(os.Getpid())))
	}
	if _, err := fmt.Fprint(os.Stdout, "ready"); err != nil {
		os.Exit(126)
	}
	time.Sleep(time.Minute)
}

func runTestCancellationExit(behavior, marker string) {
	switch behavior {
	case "status":
		os.Exit(7)
	case "status-127":
		os.Exit(127)
	case "bytes":
		writeTestCancellationBytes()
	case "inherited":
		_, err := fmt.Fprint(os.Stdout, os.Getenv("DEVSY_CANCEL_INHERITED"))
		exitTestHelperOnError(err)
	case "environment":
		_, err := fmt.Fprint(os.Stdout, marker)
		exitTestHelperOnError(err)
	case "ack-private":
		runTestCancellationAckPrivate(marker)
	default:
		os.Exit(125)
	}
}

func runTestCancellationAckPrivate(marker string) {
	root, err := os.OpenRoot(filepath.Dir(marker))
	exitTestHelperOnError(err)
	_, statErr := root.Lstat("ack-key")
	exitTestHelperOnError(root.Close())
	if !errors.Is(statErr, os.ErrNotExist) {
		os.Exit(126)
	}
	_, err = os.Stdout.Write([]byte(strings.Join(os.Environ(), "\x00")))
	exitTestHelperOnError(err)
	if runtime.GOOS == "linux" {
		proc, openErr := os.OpenRoot(fmt.Sprintf("/proc/%d", os.Getppid()))
		exitTestHelperOnError(openErr)
		for _, name := range []string{"environ", "cmdline"} {
			data, readErr := proc.ReadFile(name)
			exitTestHelperOnError(readErr)
			_, err = os.Stdout.Write(data)
			exitTestHelperOnError(err)
		}
		exitTestHelperOnError(proc.Close())
	}
}

func writeTestCancellationBytes() {
	_, err := os.Stdout.Write([]byte{0, 0xff, 0x80, '\r', '\n'})
	exitTestHelperOnError(err)
	_, err = os.Stderr.Write([]byte(os.Getenv("DEVSY_CANCEL_SECRET")))
	exitTestHelperOnError(err)
}

func writeTestCancellationBulkAfterEOF() {
	_, err := io.Copy(io.Discard, os.Stdin)
	exitTestHelperOnError(err)
	block := make([]byte, 32<<10)
	for i := range block {
		block[i] = byte(i)
	}
	for range (12 << 20) / len(block) {
		_, err := os.Stdout.Write(block)
		exitTestHelperOnError(err)
	}
	_, err = os.Stderr.Write([]byte("separate stderr"))
	exitTestHelperOnError(err)
}

func startTestCancellationOutputHolder() {
	holder := &exec.Cmd{
		Path: os.Args[0],
		Args: []string{os.Args[0], testCancellationHelperArg},
	}
	holder.Env = append(os.Environ(), "DEVSY_CANCEL_HOLDER=1")
	holder.Stdout, holder.Stderr = os.Stdout, os.Stderr
	exitTestHelperOnError(holder.Start())
	exitTestHelperOnError(testHelperWriteFile(
		os.Getenv("DEVSY_CANCEL_HOLDER_PID_PATH"), strconv.Itoa(holder.Process.Pid),
	))
	exitTestHelperOnError(holder.Process.Release())
	_, err := fmt.Fprint(os.Stdout, "ready")
	exitTestHelperOnError(err)
}

func startTestCancellationInputHolder() {
	holder := &exec.Cmd{Path: os.Args[0], Args: []string{os.Args[0], testCancellationHelperArg}}
	holder.Env = append(os.Environ(), "DEVSY_CANCEL_HOLDER=1")
	holder.Stdin = os.Stdin
	exitTestHelperOnError(holder.Start())
	exitTestHelperOnError(
		testHelperWriteFile(
			os.Getenv("DEVSY_CANCEL_HOLDER_PID_PATH"),
			strconv.Itoa(holder.Process.Pid),
		),
	)
	exitTestHelperOnError(holder.Process.Release())
	writeTestCancellationBytes()
}

func exitTestHelperOnError(err error) {
	if err != nil {
		os.Exit(126)
	}
}

func testHelperRelativePath(path string) (string, error) {
	relative, err := filepath.Rel(os.TempDir(), path)
	if err != nil {
		return "", err
	}
	if !filepath.IsLocal(relative) {
		return "", os.ErrPermission
	}
	return relative, nil
}

func testHelperWriteFile(path, data string) error {
	relative, err := testHelperRelativePath(path)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(os.TempDir())
	if err != nil {
		return err
	}
	writeErr := root.WriteFile(relative, []byte(data), 0o600)
	return errors.Join(writeErr, root.Close())
}

func awaitTestCancellationMarker(marker, acknowledgement string) {
	relative, err := testHelperRelativePath(marker)
	if err != nil {
		os.Exit(126)
	}
	root, err := os.OpenRoot(os.TempDir())
	if err != nil {
		os.Exit(126)
	}
	for {
		if info, err := root.Lstat(relative); err == nil && info.Mode().IsRegular() {
			acknowledgeTestCancellationMarker(root, acknowledgement)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func acknowledgeTestCancellationMarker(root *os.Root, acknowledgement string) {
	if acknowledgement != "" {
		exitTestHelperOnError(testHelperWriteFile(acknowledgement, "finished"))
	}
	exitTestHelperOnError(root.Close())
	os.Exit(0)
}

func testTransportCancellationHelper(behavior, marker string) {
	if err := testHelperWriteFile(os.Getenv("DEVSY_CANCEL_READY_PATH"), "ready"); err != nil {
		os.Exit(126)
	}
	go awaitTestCancellationMarker(marker, os.Getenv("DEVSY_CANCEL_ACK_PATH"))
	if behavior == testTransportRead {
		var b [1]byte
		_, _ = os.Stdin.Read(b[:])
		for {
			time.Sleep(time.Second)
		}
	}
	if runtime.GOOS != testWindowsOS {
		signal.Ignore(syscall.Signal(13))
	}
	chunk := bytes.Repeat([]byte("x"), 1024)
	for {
		if _, err := os.Stdout.Write(chunk); err != nil {
			for {
				time.Sleep(time.Second)
			}
		}
	}
}

func cancelHelperOptions(behavior string) RunCommandOptions {
	binary, err := os.Executable()
	if err != nil {
		panic(err)
	}
	return RunCommandOptions{
		Command: types.StrArray{binary, testCancellationHelperArg},
		Environ: append(
			os.Environ(),
			"DEVSY_CANCEL_HELPER=1",
			"DEVSY_CANCEL_BEHAVIOR="+behavior,
		),
		Cancellation: provider.ProviderCancellationFile,
	}
}

func TestProviderCancellationCooperatesAndCleansUp(t *testing.T) {
	ack := filepath.Join(t.TempDir(), "ack")
	opts := cancelHelperOptions("acknowledge")
	opts.Environ = append(opts.Environ, "DEVSY_CANCEL_ACK_PATH="+ack)
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	opts.Stdout = &readyWriter{ready: ready}
	opts.RawStdout = true
	finished := make(chan error, 1)
	go func() { finished <- RunCommand(ctx, opts) }()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("provider did not start")
	}
	cancel()
	select {
	case err := <-finished:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("provider did not acknowledge cancellation")
	}
	requireTestAcknowledgement(t, ack)
}

func requireTestAcknowledgement(t *testing.T, path string) {
	t.Helper()
	root, err := os.OpenRoot(filepath.Dir(path))
	require.NoError(t, err)
	contents, readErr := root.ReadFile(filepath.Base(path))
	require.NoError(t, errors.Join(readErr, root.Close()))
	require.Equal(t, "finished", string(contents))
}

type readyWriter struct{ ready chan struct{} }

func (w *readyWriter) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte("ready")) {
		select {
		case <-w.ready:
		default:
			close(w.ready)
		}
	}
	return len(p), nil
}

func TestProviderCancellationForcedAfterProductionGrace(t *testing.T) {
	opts := cancelHelperOptions("ignore")
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	opts.Stdout = &readyWriter{ready: ready}
	opts.RawStdout = true
	finished := make(chan error, 1)
	go func() { finished <- RunCommand(ctx, opts) }()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("provider did not start")
	}
	start := time.Now()
	cancel()
	select {
	case err := <-finished:
		require.ErrorIs(t, err, context.Canceled)
		require.GreaterOrEqual(t, time.Since(start), providerCancellationGrace)
	case <-time.After(providerCancellationGrace + 8*time.Second):
		t.Fatal("owned provider process was not reaped")
	}
}

type providerMarkerWriter struct {
	mu     sync.Mutex
	output bytes.Buffer
	ready  chan struct{}
}

func (w *providerMarkerWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.output.Write(p)
	if strings.HasSuffix(w.output.String(), string(filepath.Separator)+"cancel") {
		select {
		case <-w.ready:
		default:
			close(w.ready)
		}
	}
	return n, err
}

func (w *providerMarkerWriter) marker() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.output.String()
}

func TestProviderCancellationBlockedShellStdin(t *testing.T) {
	input, writer, err := os.Pipe()
	require.NoError(t, err)
	defer func() { require.NoError(t, errors.Join(input.Close(), writer.Close())) }()
	_, err = os.Stdin.Stat()
	require.NoError(t, err)

	const privateCommand = "printf '%s' \"$DEVSY_PROVIDER_CANCEL_FILE\"; mapfile lines; printf 'private-command-846307'"
	output := &providerMarkerWriter{ready: make(chan struct{})}
	opts := RunCommandOptions{
		Command:      types.StrArray{privateCommand},
		Environ:      os.Environ(),
		Stdin:        input,
		Stdout:       output,
		RawStdout:    true,
		Cancellation: provider.ProviderCancellationFile,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- RunCommand(ctx, opts) }()
	select {
	case <-output.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("provider shell did not start")
	}
	marker := output.marker()
	requirePrivateProviderScript(t, marker, privateCommand)

	start := time.Now()
	cancel()
	select {
	case err := <-finished:
		require.ErrorIs(t, err, context.Canceled)
		require.GreaterOrEqual(t, time.Since(start), providerCancellationGrace)
		require.NotContains(t, err.Error(), privateCommand)
		require.NotContains(t, err.Error(), marker)
	case <-time.After(providerCancellationGrace + providerForceAckWait + 3*time.Second):
		t.Fatal("provider shell remained blocked on stdin")
	}
	_, err = os.Stdin.Stat()
	require.NoError(t, err)
	_, err = writer.Stat()
	require.NoError(t, err)
	_, err = os.Stat(filepath.Dir(marker))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func requirePrivateProviderScript(t *testing.T, marker, command string) {
	t.Helper()
	require.True(t, filepath.IsAbs(marker))
	script := filepath.Join(filepath.Dir(marker), "script")
	info, err := os.Stat(script)
	require.NoError(t, err)
	if runtime.GOOS != testWindowsOS {
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
	root, err := os.OpenRoot(filepath.Dir(marker))
	require.NoError(t, err)
	contents, readErr := root.ReadFile("script")
	closeErr := root.Close()
	require.NoError(t, readErr)
	require.NoError(t, closeErr)
	require.Equal(t, command, string(contents))
}

func TestProviderCancellationMissingForceAck(t *testing.T) {
	dir := t.TempDir()
	completed := make(chan error, 1)
	cancelled := make(chan struct{})
	process := providerExecution{
		cancel: func(error) {
			close(cancelled)
			completed <- nil
		},
		completed: completed,
		shell: &providerShellInvocation{
			force:    filepath.Join(dir, "force"),
			ack:      filepath.Join(dir, "ack"),
			ackToken: strings.Repeat("a", 64),
		},
	}
	start := time.Now()
	err := forceProviderExecution(context.Canceled, process)
	require.ErrorContains(t, err, "provider force acknowledgement timed out")
	require.GreaterOrEqual(t, time.Since(start), providerForceAckWait)
	select {
	case <-cancelled:
	default:
		t.Fatal("provider helper was not terminated")
	}
	info, err := os.Stat(process.shell.force)
	require.NoError(t, err)
	require.True(t, info.Mode().IsRegular())
}

func TestProviderCancellationEarlyExitWithoutForceAck(t *testing.T) {
	dir := t.TempDir()
	force := filepath.Join(dir, "force")
	require.NoError(t, publishProviderMarker(force))
	completed := make(chan error, 1)
	completed <- errors.New("helper exited")
	process := providerExecution{
		cancel:    func(error) { t.Fatal("exited helper must not be terminated again") },
		completed: completed,
		shell: &providerShellInvocation{
			force:    force,
			ack:      filepath.Join(dir, "ack"),
			ackToken: strings.Repeat("a", 64),
		},
	}
	err := errors.Join(context.Canceled, waitForProviderForceAck(context.Canceled, process))
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorContains(t, err, "provider helper exited without authentic acknowledgement")
	require.NotContains(t, err.Error(), dir)
}

func TestProviderCancellationForgedAckBeforeHelperExit(t *testing.T) {
	dir := t.TempDir()
	token := strings.Repeat("a", 64)
	ack := filepath.Join(dir, "ack")
	require.NoError(t, os.WriteFile(ack, []byte(strings.Repeat("b", 64)), 0o600))
	completed := make(chan error, 1)
	completed <- errors.New("helper died")
	process := providerExecution{
		completed: completed,
		shell: &providerShellInvocation{
			force:    filepath.Join(dir, "force"),
			ack:      ack,
			ackToken: token,
		},
	}
	err := waitForProviderGrace(context.Canceled, process)
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorContains(t, err, "without authentic acknowledgement")
	require.NotContains(t, err.Error(), token)
	require.NotContains(t, err.Error(), dir)
}

func TestProviderCancellationForgedAckAfterForce(t *testing.T) {
	dir := t.TempDir()
	token := strings.Repeat("a", 64)
	ack := filepath.Join(dir, "ack")
	require.NoError(t, os.WriteFile(ack, []byte(strings.Repeat("b", 64)), 0o600))
	force := filepath.Join(dir, "force")
	require.NoError(t, publishProviderMarker(force))
	completed := make(chan error, 1)
	completed <- errors.New("helper died")
	process := providerExecution{
		completed: completed,
		shell: &providerShellInvocation{
			force:    force,
			ack:      ack,
			ackToken: token,
		},
	}
	err := errors.Join(context.Canceled, waitForProviderForceAck(context.Canceled, process))
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorContains(t, err, "without authentic acknowledgement")
	require.NotContains(t, err.Error(), token)
}

func TestProviderForceAcknowledgedRequiresPrivateExactToken(t *testing.T) {
	dir := t.TempDir()
	files := &providerShellInvocation{
		ack:      filepath.Join(dir, "ack"),
		ackToken: strings.Repeat("a", 64),
	}
	for _, content := range []string{"", strings.Repeat("b", 64), strings.Repeat("a", 63), strings.Repeat("a", 65)} {
		require.NoError(t, os.WriteFile(files.ack, []byte(content), 0o600))
		acknowledged, err := providerForceAcknowledged(files)
		require.NoError(t, err)
		require.False(t, acknowledged)
	}
	require.NoError(t, os.WriteFile(files.ack, []byte(files.ackToken), 0o600))
	acknowledged, err := providerForceAcknowledged(files)
	require.NoError(t, err)
	require.True(t, acknowledged)
	if runtime.GOOS != testWindowsOS {
		require.NoError(t, os.Chmod(files.ack, 0o400))
		acknowledged, err = providerForceAcknowledged(files)
		require.NoError(t, err)
		require.False(t, acknowledged)
	}
	require.NoError(t, os.Remove(files.ack))
	if err := os.Symlink(filepath.Join(dir, "missing"), files.ack); err == nil {
		acknowledged, err = providerForceAcknowledged(files)
		require.NoError(t, err)
		require.False(t, acknowledged)
	}
}

func TestProviderCancellationPrelaunchAndMarkerFailure(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("caller stopped")
	cancel(cause)
	require.ErrorIs(t, RunCommand(ctx, cancelHelperOptions("ignore")), cause)

	dir := t.TempDir()
	marker := filepath.Join(dir, "cancel")
	require.NoError(t, os.Mkdir(marker, 0o700))
	err := publishProviderCancellation(marker)
	require.ErrorContains(t, err, "not a regular file")
	require.NotContains(t, err.Error(), dir)

	require.NoError(t, os.Remove(marker))
	require.NoError(t, publishProviderCancellation(marker))
	require.NoError(t, publishProviderCancellation(marker))
	require.NoError(t, os.Remove(marker))
	if err := os.Symlink(filepath.Join(dir, "missing"), marker); err == nil {
		require.ErrorContains(t, publishProviderCancellation(marker), "not a regular file")
	}
}

func TestProviderCancellationPublicationFailureReapsProvider(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opts := cancelHelperOptions("ignore")
	opts.RawStdout = true
	ready := make(chan struct{})
	opts.Stdout = &readyWriter{ready: ready}
	markerPath := make(chan string, 1)
	finished := make(chan error, 1)
	go func() {
		finished <- runProviderWithCancellationUsingPublisher(ctx, opts, func(marker string) error {
			markerPath <- marker
			return &os.PathError{Op: "open", Path: marker, Err: os.ErrPermission}
		})
	}()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("provider did not start")
	}
	cancel()
	select {
	case err := <-finished:
		require.ErrorIs(t, err, context.Canceled)
		require.ErrorIs(t, err, os.ErrPermission)
		marker := <-markerPath
		require.NotContains(t, err.Error(), marker)
		_, statErr := os.Stat(filepath.Dir(marker))
		require.ErrorIs(t, statErr, os.ErrNotExist)
	case <-time.After(5 * time.Second):
		t.Fatal("provider did not stop after publication failure")
	}
}

func TestProviderCancellationPrivateEnvironment(t *testing.T) {
	dir, err := createProviderInvocationDir()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(dir)) })
	require.True(t, filepath.IsAbs(dir))
	if runtime.GOOS != testWindowsOS {
		info, err := os.Stat(dir)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	}

	opts := cancelHelperOptions("environment")
	opts.Environ = append(
		opts.Environ,
		config.EnvProviderCancelFile+"=forged",
		config.EnvProviderCancelFile+"=again",
	)
	if runtime.GOOS == testWindowsOS {
		opts.Environ = append(
			opts.Environ,
			strings.ToLower(config.EnvProviderCancelFile)+"=windows-forged",
		)
	}
	var stdout bytes.Buffer
	opts.Stdout = &stdout
	require.NoError(t, RunCommand(context.Background(), opts))
	marker := stdout.String()
	require.NotContains(t, marker, "forged")
	require.True(t, filepath.IsAbs(marker))
	_, err = os.Stat(filepath.Dir(marker))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestProviderCancellationAckKeyIsPrivate(t *testing.T) {
	dir, err := createProviderInvocationDir()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(dir)) })
	binary, err := os.Executable()
	require.NoError(t, err)
	command := fmt.Sprintf("'%s' %s", binary, testCancellationHelperArg)
	files, err := prepareProviderShell(dir, command)
	require.NoError(t, err)
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	info, statErr := root.Lstat("ack-key")
	key, readErr := root.ReadFile("ack-key")
	closeErr := root.Close()
	require.NoError(t, statErr)
	require.NoError(t, readErr)
	require.NoError(t, closeErr)
	require.True(t, info.Mode().IsRegular())
	if runtime.GOOS != testWindowsOS {
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
	require.Regexp(t, `^[0-9a-f]{64}$`, string(key))
	require.Equal(t, files.ackToken, string(key))
	var stdout bytes.Buffer
	opts := RunCommandOptions{
		Command: types.StrArray{command},
		Environ: providerCancellationEnvironment(
			[]string{"DEVSY_CANCEL_HELPER=1", "DEVSY_CANCEL_BEHAVIOR=ack-private"},
			filepath.Join(dir, "cancel"),
		),
		providerShell: files,
	}
	require.NoError(t, runSupervisedProviderShell(context.Background(), opts, &stdout, io.Discard))
	require.NotContains(t, stdout.String(), files.ackToken)
	require.NotContains(t, stdout.String(), "DEVSY_PROVIDER_SHELL_ACK=")
	_, err = os.Lstat(filepath.Join(dir, "ack-key"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestProviderCancellationAckKeyWriteFailure(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "ack-key"), 0o700))
	_, err := prepareProviderShell(dir, "printf should-not-run")
	require.ErrorContains(t, err, "write provider acknowledgement key")
	require.NotContains(t, err.Error(), dir)
}

func TestProviderCancellationShellCannotForgeAck(t *testing.T) {
	if runtime.GOOS == testWindowsOS {
		t.Skip("requires POSIX path expansion")
	}
	binary, err := os.Executable()
	require.NoError(t, err)
	forged := strings.Repeat("b", 64)
	command := fmt.Sprintf(
		"printf '%%s' '%s' > \"${DEVSY_PROVIDER_CANCEL_FILE%%/*}/ack\"; "+
			"DEVSY_CANCEL_HELPER=1 DEVSY_CANCEL_BEHAVIOR=ignore '%s' -test.run=^TestProviderCancellationHelper$",
		forged,
		binary,
	)
	pidPath := filepath.Join(t.TempDir(), "child-pid")
	opts := RunCommandOptions{
		Command:      types.StrArray{command},
		Environ:      append(os.Environ(), "DEVSY_CANCEL_CHILD_PID_PATH="+pidPath),
		Cancellation: provider.ProviderCancellationFile,
		RawStdout:    true,
	}
	ready := make(chan struct{})
	opts.Stdout = &readyWriter{ready: ready}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- RunCommand(ctx, opts) }()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("provider direct child did not start")
	}
	start := time.Now()
	cancel()
	select {
	case err := <-finished:
		require.ErrorIs(t, err, context.Canceled)
		require.GreaterOrEqual(t, time.Since(start), providerCancellationGrace)
		require.NotContains(t, err.Error(), command)
		require.NotContains(t, err.Error(), forged)
		if err.Error() != context.Canceled.Error() {
			require.ErrorContains(t, err, "acknowledgement")
		} else {
			root, openErr := os.OpenRoot(filepath.Dir(pidPath))
			require.NoError(t, openErr)
			data, readErr := root.ReadFile("child-pid")
			closeErr := root.Close()
			require.NoError(t, readErr)
			require.NoError(t, closeErr)
			pid, parseErr := strconv.Atoi(string(data))
			require.NoError(t, parseErr)
			child, findErr := os.FindProcess(pid)
			require.NoError(t, findErr)
			require.Error(t, child.Signal(syscall.Signal(0)))
		}
	case <-time.After(providerCancellationGrace + providerForceAckWait + 3*time.Second):
		t.Fatal("provider helper did not reap its direct child or report unclean shutdown")
	}
}

func TestProviderCancellationNilEnvironmentInheritsHost(t *testing.T) {
	t.Setenv("DEVSY_CANCEL_HELPER", "1")
	t.Setenv("DEVSY_CANCEL_BEHAVIOR", "inherited")
	t.Setenv("DEVSY_CANCEL_INHERITED", "present")
	binary, err := os.Executable()
	require.NoError(t, err)
	var stdout bytes.Buffer
	require.NoError(t, RunCommand(context.Background(), RunCommandOptions{
		Command:      types.StrArray{binary, testCancellationHelperArg},
		Cancellation: provider.ProviderCancellationFile,
		Stdout:       &stdout,
	}))
	require.Equal(t, "present", stdout.String())
}

func TestProviderCancellationTypedExits(t *testing.T) {
	for _, status := range []int{7, 125, 127} {
		opts := cancelHelperOptions("status")
		if status == 125 {
			opts.Environ = append(opts.Environ, "DEVSY_CANCEL_BEHAVIOR=other")
		}
		if status == 127 {
			opts.Environ = append(opts.Environ, "DEVSY_CANCEL_BEHAVIOR=status-127")
		}
		err := RunCommand(context.Background(), opts)
		var exitErr *exec.ExitError
		require.ErrorAs(t, err, &exitErr)
		require.Equal(t, status, exitErr.ExitCode())
	}
}

func TestProviderCancellationShellExits(t *testing.T) {
	if runtime.GOOS == testWindowsOS {
		t.Skip("requires POSIX shell")
	}
	binary, err := os.Executable()
	require.NoError(t, err)
	for _, status := range []int{7, 127} {
		command := "command-that-does-not-exist-846307"
		if status == 7 {
			command = fmt.Sprintf(
				"DEVSY_CANCEL_HELPER=1 DEVSY_CANCEL_BEHAVIOR=status '%s' -test.run=^TestProviderCancellationHelper$",
				binary,
			)
		}
		err := RunCommand(context.Background(), RunCommandOptions{
			Command: types.StrArray{
				command,
			},
			Environ:      os.Environ(),
			Cancellation: provider.ProviderCancellationFile,
		})
		var shellExit interp.ExitStatus
		require.ErrorAs(t, err, &shellExit)
		require.Equal(t, status, int(shellExit))
	}
}

func TestProviderCancellationParseErrorAcknowledged(t *testing.T) {
	command := "if then fi (((( private-provider-script-846307"
	err := RunCommand(context.Background(), RunCommandOptions{
		Command:      types.StrArray{command},
		Environ:      os.Environ(),
		Cancellation: provider.ProviderCancellationFile,
	})
	var status interp.ExitStatus
	require.ErrorAs(t, err, &status)
	require.Equal(t, 1, int(status))
	require.NotContains(t, err.Error(), "without authentic acknowledgement")
	require.NotContains(t, err.Error(), command)
}

func TestProviderCancellationRawBytesAndStderrRedaction(t *testing.T) {
	opts := cancelHelperOptions("bytes")
	secret := "provider-cancel-secret-84392"
	opts.Environ = append(opts.Environ, "DEVSY_CANCEL_SECRET="+secret)
	opts.RawStdout = true
	var stdout, stderr bytes.Buffer
	opts.Stdout, opts.Stderr = &stdout, &stderr
	require.NoError(t, RunCommand(context.Background(), opts))
	require.Equal(t, []byte{0, 0xff, 0x80, '\r', '\n'}, stdout.Bytes())
	require.NotContains(t, stderr.String(), secret)
	require.Contains(t, stderr.String(), "***")
}

func TestProviderCancellationArgvTransportCompletesWithOpenInput(t *testing.T) {
	local, remote := net.Pipe()
	t.Cleanup(func() {
		_ = local.Close()
		_ = remote.Close()
	})
	opts := cancelHelperOptions("bytes")
	opts.Stdin, opts.closeInputOnExit = remote, remote
	opts.RawStdout = true
	var stdout bytes.Buffer
	opts.Stdout = &stdout
	finished := make(chan error, 1)
	go func() { finished <- RunCommand(context.Background(), opts) }()
	select {
	case err := <-finished:
		require.NoError(t, err)
		require.Equal(t, []byte{0, 0xff, 0x80, '\r', '\n'}, stdout.Bytes())
	case <-time.After(5 * time.Second):
		t.Fatal("argv provider completion waited for transport input closure")
	}
}

func TestProviderCancellationArgvOwnedInputPreservesExitStatus(t *testing.T) {
	local, remote := net.Pipe()
	t.Cleanup(func() {
		_ = local.Close()
		_ = remote.Close()
	})
	opts := cancelHelperOptions("status")
	opts.Stdin, opts.closeInputOnExit = remote, remote
	finished := make(chan error, 1)
	go func() { finished <- RunCommand(context.Background(), opts) }()
	select {
	case err := <-finished:
		var exitErr *exec.ExitError
		require.ErrorAs(t, err, &exitErr)
		require.Equal(t, 7, exitErr.ExitCode())
	case <-time.After(5 * time.Second):
		t.Fatal("argv provider exit waited for transport input closure")
	}
}

func TestProviderCancellationOwnedInputPreservesShellExitStatus(t *testing.T) {
	input := io.NopCloser(strings.NewReader(""))
	err := RunCommand(context.Background(), RunCommandOptions{
		Command:          types.StrArray{"exit 7"},
		Environ:          os.Environ(),
		Stdin:            input,
		Cancellation:     provider.ProviderCancellationFile,
		closeInputOnExit: input,
	})
	var status interp.ExitStatus
	require.ErrorAs(t, err, &status)
	require.Equal(t, 7, int(status))
}

func TestProviderCancellationOwnedInputSuccessfulExitIgnoresEPIPE(t *testing.T) {
	input := io.NopCloser(bytes.NewReader(bytes.Repeat([]byte("x"), 2<<20)))
	err := RunCommand(context.Background(), RunCommandOptions{
		Command:          types.StrArray{"exit 0"},
		Environ:          os.Environ(),
		Stdin:            input,
		Cancellation:     provider.ProviderCancellationFile,
		closeInputOnExit: input,
	})
	require.NoError(t, err)
}

func TestProviderCancellationOwnedInputStopsWithInheritedStdin(t *testing.T) {
	binary, err := os.Executable()
	require.NoError(t, err)
	holderPath := filepath.Join(t.TempDir(), "holder-pid")
	t.Setenv("DEVSY_CANCEL_HOLDER_PID_PATH", holderPath)
	t.Cleanup(func() { cleanupTestCancellationOutputHolder(holderPath) })
	input := io.NopCloser(bytes.NewReader(bytes.Repeat([]byte("x"), 2<<20)))
	cmd := &exec.Cmd{Path: binary, Args: []string{binary, testCancellationHelperArg}}
	cmd.Env = append(os.Environ(), "DEVSY_CANCEL_HELPER=1", "DEVSY_CANCEL_BEHAVIOR=orphan-input")
	cmd.Stdin = input
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	finished := make(chan error, 1)
	go func() {
		finished <- waitSupervisedProviderShellWithOwnedInput(context.Background(), cmd, input, "test")
	}()
	select {
	case err := <-finished:
		require.NoError(t, err)
		require.Equal(t, []byte{0, 0xff, 0x80, '\r', '\n'}, stdout.Bytes())
	case <-time.After(5 * time.Second):
		t.Fatal("provider completion waited for inherited stdin")
	}
}

func TestProviderCancellationOwnedInputDrainsBinaryOutputAfterEOF(t *testing.T) {
	if runtime.GOOS == testWindowsOS {
		t.Skip("requires POSIX shell")
	}
	binary, err := os.Executable()
	require.NoError(t, err)
	opts := cancelHelperOptions("bulk-after-eof")
	opts.Command = types.StrArray{
		fmt.Sprintf("'%s' -test.run=^TestProviderCancellationHelper$", binary),
	}
	input := io.NopCloser(strings.NewReader(""))
	opts.Stdin = input
	opts.closeInputOnExit = input
	opts.RawStdout = true
	var stdout, stderr bytes.Buffer
	opts.Stdout, opts.Stderr = &stdout, &stderr
	require.NoError(t, RunCommand(context.Background(), opts))

	block := make([]byte, 32<<10)
	for i := range block {
		block[i] = byte(i)
	}
	require.Equal(t, 12<<20, stdout.Len())
	require.True(t, bytes.Equal(bytes.Repeat(block, (12<<20)/len(block)), stdout.Bytes()))
	require.Equal(t, "separate stderr", stderr.String())
}

type slowProviderOutputWriter struct {
	bytes.Buffer
	once sync.Once
}

func (w *slowProviderOutputWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { time.Sleep(3 * time.Second) })
	return w.Buffer.Write(p)
}

func TestProviderCancellationOwnedInputPreservesSlowOutput(t *testing.T) {
	input := io.NopCloser(strings.NewReader(""))
	var output slowProviderOutputWriter
	err := RunCommand(context.Background(), RunCommandOptions{
		Command:          types.StrArray{"printf ready"},
		Environ:          os.Environ(),
		Stdin:            input,
		Stdout:           &output,
		RawStdout:        true,
		Cancellation:     provider.ProviderCancellationFile,
		closeInputOnExit: input,
	})
	require.NoError(t, err)
	require.Equal(t, "ready", output.String())
}

func TestProviderCancellationOwnedOutputStopsAfterForcedCancellation(t *testing.T) {
	if runtime.GOOS == testWindowsOS {
		t.Skip("requires POSIX shell")
	}
	binary, err := os.Executable()
	require.NoError(t, err)
	holderPath := filepath.Join(t.TempDir(), "holder-pid")
	t.Setenv("DEVSY_CANCEL_HOLDER_PID_PATH", holderPath)
	t.Cleanup(func() { cleanupTestCancellationOutputHolder(holderPath) })
	opts := cancelHelperOptions("orphan-output")
	opts.Command = types.StrArray{
		fmt.Sprintf("'%s' -test.run=^TestProviderCancellationHelper$", binary),
	}
	input := io.NopCloser(strings.NewReader(""))
	opts.Stdin, opts.closeInputOnExit = input, input
	opts.RawStdout = true
	ready := make(chan struct{})
	opts.Stdout = &readyWriter{ready: ready}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- RunCommand(ctx, opts) }()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("provider output holder did not start")
	}
	cancel()
	select {
	case err := <-finished:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(providerCancellationGrace + providerForceAckWait + 3*time.Second):
		t.Fatal("forced provider completion waited for descendant output pipe")
	}
}

func TestProviderCancellationOwnedOutputStopsAfterSuccessfulShellExit(t *testing.T) {
	if runtime.GOOS == testWindowsOS {
		t.Skip("requires POSIX shell")
	}
	binary, err := os.Executable()
	require.NoError(t, err)
	holderPath := filepath.Join(t.TempDir(), "holder-pid")
	t.Setenv("DEVSY_CANCEL_HOLDER_PID_PATH", holderPath)
	t.Cleanup(func() { cleanupTestCancellationOutputHolder(holderPath) })
	opts := cancelHelperOptions("orphan-output")
	opts.Command = types.StrArray{
		fmt.Sprintf("'%s' -test.run=^TestProviderCancellationHelper$", binary),
	}
	input := io.NopCloser(strings.NewReader(""))
	opts.Stdin, opts.closeInputOnExit = input, input
	opts.RawStdout = true
	start := time.Now()
	err = RunCommand(context.Background(), opts)
	require.ErrorIs(t, err, exec.ErrWaitDelay)
	require.Less(t, time.Since(start), 20*time.Second)
}

func cleanupTestCancellationOutputHolder(path string) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return
	}
	data, readErr := root.ReadFile("holder-pid")
	_ = root.Close()
	if readErr != nil {
		return
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		return
	}
	process, err := os.FindProcess(pid)
	if err == nil {
		_ = process.Kill()
	}
}

func TestMachineCommandTransportCancellationJoinsCallback(t *testing.T) {
	for _, behavior := range []string{testTransportRead, "transport-write"} {
		t.Run(behavior, func(t *testing.T) {
			dir := t.TempDir()
			ready, ack := filepath.Join(dir, "ready"), filepath.Join(dir, "ack")
			t.Setenv("DEVSY_CANCEL_HELPER", "1")
			t.Setenv("DEVSY_CANCEL_BEHAVIOR", behavior)
			t.Setenv("DEVSY_CANCEL_READY_PATH", ready)
			t.Setenv("DEVSY_CANCEL_ACK_PATH", ack)
			machine := &provider.Machine{ID: "cancel-machine", Context: testLocksContext}
			cfg := &provider.ProviderConfig{
				Name: testCancelProvider,
				Exec: provider.ProviderCommands{
					Create: types.StrArray{
						testProviderCreateCommand,
					},
					Command:      cancelHelperOptions(behavior).Command,
					Cancellation: provider.ProviderCancellationFile,
				},
			}
			devsy := &config.Config{
				DefaultContext: testLocksContext,
				Contexts:       map[string]*config.ContextConfig{testLocksContext: {}},
			}
			mc, err := NewMachineClient(devsy, cfg, machine)
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			conn, err := mc.OpenCommandTransport(
				ctx,
				client.CommandTransportOptions{Command: testOpaqueCommand},
			)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, conn.Close()) })
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err := os.Stat(ready); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("transport provider did not start")
				}
				time.Sleep(10 * time.Millisecond)
			}
			cancel()
			completed := make(chan error, 1)
			go func() { completed <- conn.Wait() }()
			select {
			case err := <-completed:
				require.ErrorIs(t, err, context.Canceled)
			case <-time.After(5 * time.Second):
				t.Fatal("transport callback did not finish")
			}
			requireTestAcknowledgement(t, ack)
		})
	}
}

func TestWorkspaceCommandCancellationJoinsProviderCleanup(t *testing.T) {
	for _, transportMode := range []bool{false, true} {
		t.Run(fmt.Sprint("transport=", transportMode), func(t *testing.T) {
			ack := filepath.Join(t.TempDir(), "ack")
			t.Setenv("DEVSY_CANCEL_HELPER", "1")
			t.Setenv("DEVSY_CANCEL_BEHAVIOR", "acknowledge")
			t.Setenv("DEVSY_CANCEL_ACK_PATH", ack)
			wc := workspaceCancellationTestClient()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if transportMode {
				requireWorkspaceTransportCancellation(t, wc, ctx, cancel)
			} else {
				requireWorkspaceDirectCancellation(t, wc, ctx, cancel)
			}
			requireTestAcknowledgement(t, ack)
		})
	}
}

func TestWorkspaceTransportCompletesWithoutClosingInput(t *testing.T) {
	wc := workspaceCancellationTestClient()
	wc.providerConfig.Exec.Command = types.StrArray{"printf ready"}
	conn, err := wc.OpenCommandTransport(context.Background(), client.CommandTransportOptions{
		Command: testOpaqueCommand,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	ready := make([]byte, 5)
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, err = io.ReadFull(conn, ready)
	require.NoError(t, err)
	require.Equal(t, "ready", string(ready))
	finished := make(chan error, 1)
	go func() { finished <- conn.Wait() }()
	select {
	case err := <-finished:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("provider completion waited for caller to close transport input")
	}
}

func workspaceCancellationTestClient() *workspaceClient {
	return &workspaceClient{
		providerWorkspace: &provider.Workspace{
			ID:       "cancel-workspace",
			Context:  testLocksContext,
			Provider: provider.WorkspaceProviderConfig{Name: testCancelProvider},
		},
		providerConfig: &provider.ProviderConfig{
			Name: testCancelProvider,
			Exec: provider.ProviderCommands{
				Command:      cancelHelperOptions("acknowledge").Command,
				Cancellation: provider.ProviderCancellationFile,
			},
		},
		devsyConfig: &config.Config{
			DefaultContext: testLocksContext,
			Contexts:       map[string]*config.ContextConfig{testLocksContext: {}},
		},
	}
}

func requireWorkspaceTransportCancellation(
	t *testing.T,
	wc *workspaceClient,
	ctx context.Context,
	cancel context.CancelFunc,
) {
	t.Helper()
	conn, err := wc.OpenCommandTransport(ctx, client.CommandTransportOptions{
		Command: testOpaqueCommand,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	ready := make([]byte, 5)
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, err = io.ReadFull(conn, ready)
	require.NoError(t, err)
	require.Equal(t, "ready", string(ready))
	cancel()
	require.ErrorIs(t, conn.Wait(), context.Canceled)
}

func requireWorkspaceDirectCancellation(
	t *testing.T,
	wc *workspaceClient,
	ctx context.Context,
	cancel context.CancelFunc,
) {
	t.Helper()
	ready := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		finished <- wc.Command(ctx, client.CommandOptions{
			Command:   testOpaqueCommand,
			Stdout:    &readyWriter{ready: ready},
			RawStdout: true,
		})
	}()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("workspace provider did not start")
	}
	cancel()
	select {
	case err := <-finished:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("workspace provider did not finish")
	}
}
