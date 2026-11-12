package docker

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/livetee"
	"github.com/devsy-org/devsy/pkg/subprocess"
	"github.com/stretchr/testify/suite"
)

const liveOutputWindows = "windows"

type liveOutputSuite struct {
	suite.Suite
	script string
}

func TestLiveOutputSuite(t *testing.T) { suite.Run(t, new(liveOutputSuite)) }

type blockedOutput struct {
	entered      chan struct{}
	release      chan struct{}
	finished     chan struct{}
	once         sync.Once
	onceFinished sync.Once
	mu           sync.Mutex
	received     string
}

func (w *blockedOutput) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.release
	w.mu.Lock()
	w.received += string(p)
	w.mu.Unlock()
	w.onceFinished.Do(func() { close(w.finished) })
	return len(p), nil
}

type safeBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(p)
}
func (b *safeBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.Buffer.String() }

func (s *liveOutputSuite) TestRunBlocked() {
	s.blocked(func(ctx context.Context, h *DockerHelper, streams Streams) error {
		return h.Run(ctx, []string{s.script}, streams)
	})
}

func (s *liveOutputSuite) TestRunWithDirBlocked() {
	s.blocked(func(ctx context.Context, h *DockerHelper, streams Streams) error {
		return h.RunWithDir(ctx, "", []string{s.script}, streams)
	})
}

func (s *liveOutputSuite) TestRunWithEnvBlocked() {
	s.blocked(func(ctx context.Context, h *DockerHelper, streams Streams) error {
		return h.RunWithEnv(ctx, nil, []string{s.script}, streams)
	})
}

func (s *liveOutputSuite) TestCaptureOnly() {
	h := s.helper("printf 'one\\ntwo\\nthree\\n'; printf 'error\\n' >&2\n")
	var out, stderr bytes.Buffer
	s.Require().
		NoError(h.Run(context.Background(), []string{s.script}, Streams{Stdout: &out, Stderr: &stderr}))
	s.Equal("one\ntwo\nthree\n", out.String())
	s.Equal("error\n", stderr.String())
}

func (s *liveOutputSuite) TestSharedCapture() {
	h := s.helper(
		"i=0; while [ $i -lt 200 ]; do printf 'out\\n'; printf 'err\\n' >&2; i=$((i+1)); done\n",
	)
	var capture bytes.Buffer
	err := h.Run(context.Background(), []string{s.script}, Streams{
		Stdout: &capture, Stderr: &capture, LiveStdout: io.Discard, LiveStderr: io.Discard,
	})
	s.Require().NoError(err)
	s.Equal(200, strings.Count(capture.String(), "out\n"))
	s.Equal(200, strings.Count(capture.String(), "err\n"))
}

func (s *liveOutputSuite) TestSharedCaptureAndLiveDestination() {
	h := s.helper(
		"i=0; while [ $i -lt 200 ]; do printf 'out\\n'; printf 'err\\n' >&2; i=$((i+1)); done\n",
	)
	var capture bytes.Buffer
	err := h.Run(
		context.Background(),
		[]string{s.script},
		Streams{Stdout: &capture, LiveStderr: &capture},
	)
	s.Require().NoError(err)
	s.Equal(200, strings.Count(capture.String(), "out\n"))
	s.Equal(200, strings.Count(capture.String(), "err\n"))
}

func (s *liveOutputSuite) TestSameCaptureAndLiveStreamWritesOnce() {
	h := s.helper("printf 'once\\n'\n")
	var captured bytes.Buffer
	s.Require().
		NoError(h.Run(context.Background(), []string{s.script}, Streams{Stdout: &captured, LiveStdout: &captured}))
	s.Equal("once\n", captured.String())
}

func (s *liveOutputSuite) TestLiveRedaction() {
	h := s.helper("printf 'secretprefix'; printf 'secretsuffix\\n'\n")
	var captured, live bytes.Buffer
	err := h.RunWithEnv(
		context.Background(),
		nil,
		[]string{s.script},
		Streams{
			Stdout:       &captured,
			LiveStdout:   &live,
			Stderr:       io.Discard,
			RedactionEnv: []string{"DEVSY_TEST_TOKEN=secretprefixsecretsuffix"},
		},
	)
	s.Require().NoError(err)
	s.Equal("secretprefixsecretsuffix\n", captured.String())
	s.NotContains(live.String(), "secretprefixsecretsuffix")
	s.True(strings.Contains(live.String(), "***"), live.String())
}

type sliceOutput []byte

func (w sliceOutput) Write(p []byte) (int, error) { return len(p), nil }

type nestedSliceOutput struct{ io.Writer }

func (s *liveOutputSuite) TestNonComparableWriterDoesNotPanic() {
	h := s.helper("printf 'output\\n'\n")
	writer := nestedSliceOutput{Writer: sliceOutput{1}}
	s.NotPanics(func() {
		s.Require().
			NoError(h.Run(context.Background(), []string{s.script}, Streams{Stdout: writer, LiveStdout: writer}))
	})
}

func (s *liveOutputSuite) TestFileLiveOutputUsesFileDirectly() {
	file, err := os.CreateTemp(s.T().TempDir(), "output")
	s.Require().NoError(err)
	defer func() { _ = file.Close() }()
	tee := livetee.New(io.Discard, nil)
	cmd := &exec.Cmd{}
	var stream *subprocess.StreamingRedactingWriter
	cmd.Stdout, stream = attachDockerStream(nil, file, tee, nil)
	s.Same(file, cmd.Stdout)
	s.Nil(stream)
	s.NoError(tee.Finish())
}

func (s *liveOutputSuite) TestFileLiveCancellationSkipsFinalStatus() {
	h := s.helper("printf 'ready\\n'; exec sleep 60\n")
	reader, writer, err := os.Pipe()
	s.Require().NoError(err)
	defer func() { _ = reader.Close(); _ = writer.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.Run(ctx, []string{s.script}, Streams{LiveStdout: writer}) }()
	ready, err := bufio.NewReader(reader).ReadString('\n')
	s.Require().NoError(err)
	s.Equal("ready\n", ready)
	cancel()
	runErr := s.waitError(done)
	s.ErrorIs(runErr, context.Canceled)
	marker, ok := errors.AsType[interface {
		error
		SkipFinalStatus() bool
	}](runErr)
	s.Require().True(ok)
	s.True(marker.SkipFinalStatus())
}

func (s *liveOutputSuite) TestNonFileLiveOutputUsesTee() {
	var live bytes.Buffer
	tee := livetee.New(io.Discard, nil)
	cmd := &exec.Cmd{}
	var stream *subprocess.StreamingRedactingWriter
	cmd.Stdout, stream = attachDockerStream(nil, &live, tee, nil)
	s.NotSame(&live, cmd.Stdout)
	s.NotNil(stream)
	_, err := cmd.Stdout.Write([]byte("output"))
	s.Require().NoError(err)
	s.Require().NoError(stream.Flush())
	s.Require().NoError(tee.Finish())
	s.Equal("output", live.String())
}

func (s *liveOutputSuite) helper(script string) *DockerHelper {
	if runtime.GOOS == liveOutputWindows {
		s.T().Skip("shell fixture requires Unix")
	}
	p := filepath.Join(s.T().TempDir(), "docker")
	s.Require().NoError(os.WriteFile(p, []byte(script), 0o600))
	s.script = p
	return &DockerHelper{DockerCommand: "/bin/sh"}
}

func (s *liveOutputSuite) blocked(run func(context.Context, *DockerHelper, Streams) error) {
	h := s.helper("printf 'stdout line\\n'; printf 'stderr line\\n' >&2; exec sleep 60\n")
	w := &blockedOutput{
		entered:  make(chan struct{}),
		release:  make(chan struct{}),
		finished: make(chan struct{}),
	}
	var out, stderr safeBuffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(ctx, h, Streams{Stdout: &out, Stderr: &stderr, LiveStdout: w, LiveStderr: w}) }()
	var release sync.Once
	s.T().Cleanup(func() { release.Do(func() { close(w.release) }) })
	s.wait(w.entered)
	s.Require().
		Eventually(func() bool {
			return out.String() == "stdout line\n" && stderr.String() == "stderr line\n"
		}, 5*time.Second, 10*time.Millisecond)
	start := time.Now()
	cancel()
	err := s.waitError(done)
	s.Less(time.Since(start), time.Second)
	s.Require().ErrorContains(err, "bytes dropped")
	s.Contains(err.Error(), "drain abandoned: true")
	s.Equal("stdout line\n", out.String())
	s.Equal("stderr line\n", stderr.String())
	release.Do(func() { close(w.release) })
	s.wait(w.finished)
	w.mu.Lock()
	received := w.received
	w.mu.Unlock()
	s.NotContains(received, "bytes dropped")
	s.T().
		Log("returned within 1s after cancellation; stdout and stderr capture complete; dropped bytes reported")
}

func (s *liveOutputSuite) wait(ch <-chan struct{}) {
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		s.FailNow("channel deadline exceeded")
	}
}

func (s *liveOutputSuite) waitError(ch <-chan error) error {
	select {
	case err := <-ch:
		return err
	case <-time.After(2 * time.Second):
		s.FailNow("command did not return")
		return nil
	}
}
