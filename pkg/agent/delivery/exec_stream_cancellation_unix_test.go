//go:build linux || darwin

package delivery

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/driver/microsandbox"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const execStreamCancellationTestBound = 3 * time.Second

type cancellationBinary struct {
	io.Reader
	closer  io.Closer
	entered chan struct{}
	once    sync.Once
	closes  atomic.Int32
}

func (s *cancellationBinary) Read(p []byte) (int, error) {
	s.once.Do(func() { close(s.entered) })
	return s.Reader.Read(p)
}

func (s *cancellationBinary) Close() error {
	s.closes.Add(1)
	if s.closer != nil {
		return s.closer.Close()
	}
	return nil
}

type cancellationBinarySource struct {
	mu       sync.Mutex
	blocked  int
	sources  []*cancellationBinary
	writers  []*io.PipeWriter
	acquired chan *cancellationBinary
}

func (s *cancellationBinarySource) acquire(ctx context.Context, _ string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Cleanup cancels before taking this mutex; a concurrent retry must not
	// allocate a new blocked reader after cleanup has closed the existing ones.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	source := &cancellationBinary{entered: make(chan struct{})}
	if len(s.sources) < s.blocked {
		reader, writer := io.Pipe()
		source.Reader, source.closer = reader, reader
		s.writers = append(s.writers, writer)
	} else {
		source.Reader = strings.NewReader("fresh-source-binary")
	}
	s.sources = append(s.sources, source)
	s.acquired <- source
	return source, nil
}

func (s *cancellationBinarySource) cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, source := range s.sources {
		if source.closer != nil {
			_ = source.closer.Close()
		}
	}
	for _, writer := range s.writers {
		_ = writer.Close()
	}
}

func (s *cancellationBinarySource) assertOwnership(t *testing.T, expected int) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	require.Len(t, s.sources, expected, "every retry must acquire a fresh source")
	for index, source := range s.sources {
		select {
		case <-source.entered:
		default:
			t.Errorf("source %d was acquired but never read", index)
		}
		assert.EqualValues(t, 1, source.closes.Load(), "source %d close count", index)
		if index > 0 {
			assert.NotSame(t, s.sources[index-1], source, "retry reused the previous source")
		}
	}
}

func cancellationDeliveryRuntime(t *testing.T, blockCompletion bool) driver.ArgvExecDriver {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nexec /bin/cat\n"
	if blockCompletion {
		// A stage's small finite source drains into the pipe, but its process
		// remains alive. CommandContext must kill it on the real completion timer.
		script = "#!/bin/sh\ncase \"$*\" in *\"cat >\"*) exec /bin/sleep 60;; *) exit 0;; esac\n"
	}
	// #nosec G306 -- Fixed fake executable in the test's private temporary directory.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "msb"), []byte(script), 0o700))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	runtime, err := microsandbox.NewMicrosandboxDriver(
		context.Background(),
		&provider.AgentWorkspaceInfo{},
	)
	require.NoError(t, err)
	argvRuntime, ok := runtime.(driver.ArgvExecDriver)
	require.True(t, ok)
	return argvRuntime
}

func cancellationDeliveryExecutor(
	t *testing.T, runtime driver.ArgvExecDriver, canceled chan<- error,
) func(context.Context, []string, driver.Streams) error {
	return func(ctx context.Context, argv []string, streams driver.Streams) error {
		err := runtime.CommandContainerArgv(ctx, "owned-delivery-test", argv, streams)
		if streams.Stdin != nil {
			if cause := context.Cause(ctx); cause != nil {
				select {
				case canceled <- cause:
				default:
					t.Error("unexpected extra attempt cancellation")
				}
			}
		}
		return err
	}
}

type cancellationDeliveryCall struct {
	done   chan struct{}
	cancel context.CancelFunc
	err    error
}

func startCancellationDelivery(
	t *testing.T, delivery *KubernetesDelivery, sources *cancellationBinarySource,
) (context.Context, *cancellationDeliveryCall) {
	t.Helper()
	outer, cancel := context.WithCancel(context.Background())
	call := &cancellationDeliveryCall{done: make(chan struct{}), cancel: cancel}
	t.Cleanup(func() {
		cancel()
		sources.cleanup()
		select {
		case <-call.done:
		case <-time.After(execStreamCancellationTestBound):
			t.Error("delivery failed to join after explicit source cleanup")
		}
	})
	go func() {
		defer close(call.done)
		// The fake msb never interprets this stage destination or writes to it.
		call.err = delivery.deliverViaExecStream(
			outer,
			"/tmp/owned-delivery-never-written",
			PostStartOptions{
				Arch: "amd64", SkipVersionCheck: true, BinarySource: sources.acquire,
			},
		)
	}()
	return outer, call
}

func awaitDeliveryRead(t *testing.T, sources *cancellationBinarySource) {
	t.Helper()
	select {
	case source := <-sources.acquired:
		select {
		case <-source.entered:
		case <-time.After(execStreamCancellationTestBound):
			t.Fatal("actual source Read not entered")
		}
	case <-time.After(execStreamCancellationTestBound):
		t.Fatal("source not acquired")
	}
}

func TestExecStreamAttemptCancellationOwnsSource(t *testing.T) {
	for _, testCase := range []struct {
		name            string
		blockedSources  int
		blockCompletion bool
		cause           error
		result          error
		acquisitions    int
	}{
		{"retry acquires fresh source", 1, false, errExecStreamIdleTimeout, nil, 2},
		{"idle retries exhausted", 2, false, errExecStreamIdleTimeout, errExecStreamIdleTimeout, 2},
		{"completion retries exhausted", 0, true, errExecStreamCompletionTimeout, errExecStreamCompletionTimeout, 2},
		{"normal completion", 0, false, nil, nil, 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			runtime := cancellationDeliveryRuntime(t, testCase.blockCompletion)
			sources := &cancellationBinarySource{
				blocked:  testCase.blockedSources,
				acquired: make(chan *cancellationBinary, execStreamMaxAttempts),
			}
			canceled := make(chan error, execStreamMaxAttempts)
			delivery := &KubernetesDelivery{
				execStreamIdleTimeout:       300 * time.Millisecond,
				execStreamCompletionTimeout: 300 * time.Millisecond,
				Exec:                        cancellationDeliveryExecutor(t, runtime, canceled),
			}
			outer, call := startCancellationDelivery(t, delivery, sources)
			awaitDeliveryRead(t, sources)
			if testCase.cause != nil {
				select {
				case cause := <-canceled:
					require.ErrorIs(t, cause, testCase.cause)
				case <-call.done:
					select {
					case cause := <-canceled:
						require.ErrorIs(t, cause, testCase.cause)
					default:
						t.Fatalf("delivery returned before attempt cancellation: %v", call.err)
					}
				case <-time.After(execStreamCancellationTestBound):
					t.Fatal("actual attempt cancellation was not observed")
				}
			}
			select {
			case <-call.done:
			case <-time.After(execStreamCancellationTestBound):
				t.Fatal("delivery remained blocked after actual attempt cancellation")
			}
			if testCase.result == nil {
				require.NoError(t, call.err)
			} else {
				require.ErrorIs(
					t,
					call.err,
					testCase.result,
					"original attempt cause must survive cleanup and retry",
				)
			}
			require.NoError(t, outer.Err(), "attempt timeout must not cancel the outer operation")
			call.cancel()
			sources.assertOwnership(t, testCase.acquisitions)
		})
	}
}
