package compose

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/livetee"
	"github.com/stretchr/testify/suite"
)

type liveOutputSuite struct{ suite.Suite }

func TestLiveOutputSuite(t *testing.T) { suite.Run(t, new(liveOutputSuite)) }

type blockedLiveWriter struct{ entered, release chan struct{} }

func (w *blockedLiveWriter) Write(p []byte) (int, error) {
	select {
	case <-w.entered:
	default:
		close(w.entered)
	}
	<-w.release
	return len(p), nil
}

func (s *liveOutputSuite) TestBlockedCancellationAndCapture() {
	h := s.helper("printf 'stderr line\n' >&2\nprintf 'stdout line\n'\nexec sleep 60\n")
	w := &blockedLiveWriter{entered: make(chan struct{}), release: make(chan struct{})}
	closed := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	captured := completeCapture{complete: make(chan struct{})}
	done := make(chan struct{})
	var err error
	go func() {
		err = h.RunWithOutput(
			ctx,
			nil,
			docker.Streams{Stdout: w, Stderr: w},
			Output{Capture: &captured, Close: func() error { close(closed); return nil }},
		)
		close(done)
	}()
	s.wait(w.entered)
	s.wait(captured.complete)
	start := time.Now()
	cancel()
	s.wait(done)
	s.Less(time.Since(start), livetee.DrainTimeout+time.Second)
	s.Require().ErrorContains(err, "drain abandoned: true")
	s.Contains(err.Error(), "stderr line: signal: killed")
	s.Contains(captured.String(), "stdout line\n")
	s.Contains(captured.String(), "stderr line\n")
	select {
	case <-closed:
		s.Fail("closed before Write returned")
	default:
	}
	close(w.release)
	s.wait(closed)
	s.T().Log("returned within 1100ms after cancellation; stderr and recorder capture complete")
}

func (s *liveOutputSuite) TestSuccessAndFailureOutput() {
	for _, code := range []string{"0", "7"} {
		s.Run(code, func() {
			h := s.helper(
				"printf 'one\ntwo\nthree\n'\nprintf 'failure detail\n' >&2\nexit " + code + "\n",
			)
			var out, stderr bytes.Buffer
			err := h.Run(context.Background(), nil, docker.Streams{Stdout: &out, Stderr: &stderr})
			if code == "0" {
				s.Require().NoError(err)
			} else {
				s.EqualError(err, "failure detail: exit status 7")
			}
			s.Equal("one\ntwo\nthree\n", out.String())
			s.Equal("failure detail\n", stderr.String())
		})
	}
}

func (s *liveOutputSuite) TestRedactsFailedStderr() {
	secret := strings.Join([]string{"privateprefix", "privatesuffix"}, "")
	h := s.helper("printf 'privateprefix' >&2\nprintf 'privatesuffix' >&2\nexit 7\n")
	h.Docker = &docker.DockerHelper{Environment: []string{"DEVSY_TEST_TOKEN=" + secret}}
	var stderr, capture bytes.Buffer
	err := h.RunWithOutput(
		context.Background(),
		nil,
		docker.Streams{Stderr: &stderr},
		Output{Capture: &capture},
	)
	s.EqualError(err, "***: exit status 7")
	s.Equal("***", stderr.String())
	s.Equal("***", capture.String())
}

func (s *liveOutputSuite) wait(ch <-chan struct{}) {
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		s.FailNow("channel deadline exceeded")
	}
}

func (s *liveOutputSuite) helper(script string) *ComposeHelper {
	if runtime.GOOS == "windows" {
		s.T().Skip("requires a POSIX shell")
	}
	path := filepath.Join(s.T().TempDir(), "fake-compose")
	s.Require().NoError(os.WriteFile(path, []byte(script), 0o600))
	return &ComposeHelper{Command: "/bin/sh", Args: []string{path}}
}

type completeCapture struct {
	bytes.Buffer
	complete chan struct{}
	once     sync.Once
}

func (w *completeCapture) Write(p []byte) (int, error) {
	n, err := w.Buffer.Write(p)
	if bytes.Contains(w.Bytes(), []byte("stdout line\n")) &&
		bytes.Contains(w.Bytes(), []byte("stderr line\n")) {
		w.once.Do(func() { close(w.complete) })
	}
	return n, err
}
