//go:build linux || darwin

package microsandbox

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const execCancellationTestBound = 3 * time.Second

type cancellationInput struct {
	io.Reader
	closer       func() error
	entered      chan struct{}
	readOnce     sync.Once
	closes       atomic.Int32
	closeEntered chan struct{}
	closeGate    <-chan struct{}
}

func (r *cancellationInput) Read(p []byte) (int, error) {
	if r.entered != nil {
		r.readOnce.Do(func() { close(r.entered) })
	}
	return r.Reader.Read(p)
}

func (r *cancellationInput) Close() error {
	r.closes.Add(1)
	if r.closeEntered != nil {
		close(r.closeEntered)
	}
	if r.closeGate != nil {
		<-r.closeGate
	}
	if r.closer != nil {
		return r.closer()
	}
	return nil
}

func cancellationMSB(t *testing.T, exitImmediately bool) string {
	t.Helper()
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	script := "#!/bin/sh\nprintf 'fixture:start\\n' >&2\n" +
		"if ! printf '%s\\n' \"$$\" > \"$DEVSY_CANCEL_READY\"; then " +
		"printf 'fixture:ready-write-failed\\n' >&2; exit 2; fi\nprintf 'fixture:ready-written\\n' >&2\n"
	if exitImmediately {
		script += "exit 0\n"
	} else {
		script += "exec /bin/cat\n"
	}
	// #nosec G306 -- This executable is a fixed test-owned fake in a private temporary directory.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "msb"), []byte(script), 0o700))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DEVSY_CANCEL_READY", ready)
	return ready
}

func awaitCancellationPID(t *testing.T, path string) int {
	t.Helper()
	var pid int
	started := time.Now()
	var state atomic.Value
	state.Store("not checked")
	defer func() { t.Logf("child readiness: state=%s elapsed=%s", state.Load().(string), time.Since(started)) }()
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(path) // #nosec G304 -- Only the test-owned readiness file is read.
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				state.Store("missing")
			} else {
				state.Store("read error")
			}
			return false
		}
		pid, err = strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil || pid <= 1 {
			state.Store("invalid pid")
			return false
		}
		state.Store("ready")
		return true
	}, execCancellationTestBound, time.Millisecond, "child readiness absent")
	return pid
}

func awaitCancellationSignal(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	started := time.Now()
	defer func() { t.Logf("checkpoint %q elapsed=%s", message, time.Since(started)) }()
	select {
	case <-signal:
	case <-time.After(execCancellationTestBound):
		t.Fatal(message)
	}
}

type cancellationCall struct {
	cancel  context.CancelFunc
	done    chan struct{}
	err     error
	started time.Time
	capture *os.File
}

func startCancellationCall(t *testing.T, input io.Reader, cleanup func()) *cancellationCall {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	capture, err := os.CreateTemp(t.TempDir(), "fixture-output")
	require.NoError(t, err)
	output := struct{ io.Writer }{capture}
	call := &cancellationCall{
		cancel:  cancel,
		done:    make(chan struct{}),
		started: time.Now(),
		capture: capture,
	}
	t.Cleanup(func() {
		call.logState(t, "before cleanup", input)
		cancel()
		if cleanup != nil {
			cleanup()
		}
		select {
		case <-call.done:
			call.logState(t, "after cleanup", input)
		case <-time.After(execCancellationTestBound):
			call.logState(t, "cleanup timeout", input)
			t.Error("Exec failed to join after explicit input cleanup")
		}
		_ = capture.Close()
	})
	go func() {
		defer close(call.done)
		call.err = (cliClient{}).Exec(ctx, "owned-cancellation-test", execRequest{
			Stdin: input, Stdout: output, Stderr: output,
		})
	}()
	return call
}

func (call *cancellationCall) logState(t *testing.T, phase string, input io.Reader) {
	t.Helper()
	done := false
	var errType any
	select {
	case <-call.done:
		done = true
		errType = call.err
	default:
	}
	var closes int32
	if tracked, ok := input.(*cancellationInput); ok {
		closes = tracked.closes.Load()
	}
	t.Logf("fixture %s: elapsed=%s done=%t error_type=%T input_closes=%d output=%s",
		phase, time.Since(call.started), done, errType, closes, fixtureOutput(call.capture))
}

func fixtureOutput(file *os.File) string {
	data := make([]byte, 512)
	n, err := file.ReadAt(data, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return "read error"
	}
	text := string(data[:n])
	if text == "" {
		return "empty"
	}
	for line := range strings.SplitSeq(strings.TrimSuffix(text, "\n"), "\n") {
		switch line {
		case "fixture:start", "fixture:ready-written", "fixture:ready-write-failed", "complete":
		default:
			return "other output"
		}
	}
	return text
}

func TestExecCancellationClosesBlockedInput(t *testing.T) {
	for _, testCase := range []struct {
		name            string
		exitImmediately bool
	}{
		{name: "running child"},
		{name: "child exited while stdin copy remains blocked", exitImmediately: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ready := cancellationMSB(t, testCase.exitImmediately)
			reader, writer := io.Pipe()
			input := &cancellationInput{
				Reader:  reader,
				closer:  reader.Close,
				entered: make(chan struct{}),
			}
			call := startCancellationCall(t, input, func() {
				_ = reader.Close()
				_ = writer.Close()
			})
			awaitCancellationSignal(t, input.entered, "source Read not entered")
			pid := awaitCancellationPID(t, ready)
			if testCase.exitImmediately {
				require.Eventually(t, func() bool {
					return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
				}, execCancellationTestBound, time.Millisecond, "child did not exit before cancellation")
			}
			call.cancel()
			awaitCancellationSignal(t, call.done, "Exec stayed blocked after cancellation")
			if !testCase.exitImmediately {
				require.Error(t, call.err, "cancellation must not report successful execution")
			}
			assert.EqualValues(t, 1, input.closes.Load())
		})
	}
}

func TestExecCompletionPreservesInputOwnership(t *testing.T) {
	cancellationMSB(t, false)
	input := &cancellationInput{Reader: strings.NewReader("complete")}
	call := startCancellationCall(t, input, nil)
	awaitCancellationSignal(t, call.done, "normal execution did not finish")
	require.NoError(t, call.err)
	call.cancel()
	assert.Never(
		t,
		func() bool { return input.closes.Load() != 0 },
		50*time.Millisecond,
		time.Millisecond,
		"normal completion must disarm the cancellation callback",
	)
}

func TestExecCancellationPreservesCallerFile(t *testing.T) {
	ready := cancellationMSB(t, false)
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	call := startCancellationCall(t, reader, func() {
		_ = reader.Close()
		_ = writer.Close()
	})
	awaitCancellationPID(t, ready)
	call.cancel()
	awaitCancellationSignal(t, call.done, "file-backed Exec cancellation did not return")
	require.Error(t, call.err)
	_, err = reader.Stat()
	assert.NoError(t, err, "the caller retains ownership of its file")
}

func TestExecStartFailurePreservesInput(t *testing.T) {
	dir := t.TempDir()
	// #nosec G306 -- Intentionally executable fixture with an invalid interpreter, in a private directory.
	require.NoError(
		t,
		os.WriteFile(filepath.Join(dir, "msb"), []byte("#!/nonexistent-interpreter\n"), 0o700),
	)
	t.Setenv("PATH", dir)
	input := &cancellationInput{Reader: strings.NewReader("unused")}
	call := startCancellationCall(t, input, nil)
	awaitCancellationSignal(t, call.done, "failed Start did not return")
	require.Error(t, call.err)
	call.cancel()
	assert.Zero(t, input.closes.Load(), "failed Start must not transfer input ownership")
}

func TestExecAlreadyCanceledPreservesInput(t *testing.T) {
	cancellationMSB(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	input := &cancellationInput{Reader: strings.NewReader("unused")}
	err := (cliClient{}).Exec(ctx, "owned-cancellation-test", execRequest{Stdin: input})
	require.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, input.closes.Load(), "pre-start cancellation must not transfer input ownership")
}

func TestExecCancellationJoinsInputClose(t *testing.T) {
	ready := cancellationMSB(t, false)
	reader, writer := io.Pipe()
	gate := make(chan struct{})
	var release sync.Once
	input := &cancellationInput{
		Reader: reader, closer: reader.Close, entered: make(chan struct{}),
		closeEntered: make(chan struct{}), closeGate: gate,
	}
	call := startCancellationCall(t, input, func() {
		release.Do(func() { close(gate) })
		_ = reader.Close()
		_ = writer.Close()
	})
	awaitCancellationPID(t, ready)
	awaitCancellationSignal(t, input.entered, "source Read not entered")
	call.cancel()
	awaitCancellationSignal(t, input.closeEntered, "cancellation did not invoke Close")
	select {
	case <-call.done:
		t.Fatal("Exec returned before its Close callback completed")
	default:
	}
	release.Do(func() { close(gate) })
	awaitCancellationSignal(t, call.done, "Exec did not join the completed Close callback")
	require.Error(t, call.err)
	assert.EqualValues(t, 1, input.closes.Load())
}
