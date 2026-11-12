package git

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/livetee"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/devsy-org/devsy/pkg/subprocess"
	"github.com/stretchr/testify/suite"
)

const liveTestShell = "/bin/sh"

type liveOutputSuite struct{ suite.Suite }

func TestLiveOutputSuite(t *testing.T) { suite.Run(t, new(liveOutputSuite)) }

type blockedLiveWriter struct {
	entered chan struct{}
	release chan struct{}
	bytes.Buffer
}

func (w *blockedLiveWriter) Write(p []byte) (int, error) {
	select {
	case <-w.entered:
	default:
		close(w.entered)
	}
	<-w.release
	return w.Buffer.Write(p)
}

func (s *liveOutputSuite) TestBlockedCancellation() {
	binary := s.binary("printf 'stderr line\n' >&2\nprintf 'stdout line\n'\nexec sleep 60\n")
	w := &blockedLiveWriter{entered: make(chan struct{}), release: make(chan struct{})}
	defer close(w.release)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	var result RunResult
	var err error
	go func() {
		result, err = (execRunner{}).Run(
			ctx,
			RunOptions{Binary: liveTestShell, Args: []string{binary}, Stdout: w},
		)
		close(done)
	}()
	s.wait(w.entered)
	start := time.Now()
	cancel()
	s.wait(done)
	s.Less(time.Since(start), livetee.DrainTimeout+time.Second)
	s.Equal("stdout line\n", string(result.Stdout))
	s.Equal("stderr line\n", string(result.Stderr))
	s.Require().ErrorContains(err, "drain abandoned: true")
	s.T().Log("returned within 1100ms after cancellation; captured stdout and stderr complete")
}

func (s *liveOutputSuite) TestSplitSecretBeforeDroppedQueue() {
	secret := strings.Join([]string{"privateprefix", "privatesuffix"}, "")
	redactor := secrets.NewEnvironmentRedactor([]string{"DEVSY_TEST_TOKEN=" + secret})
	captured := subprocess.NewBuffer(redactor)
	w := &blockedLiveWriter{entered: make(chan struct{}), release: make(chan struct{})}
	released := make(chan struct{})
	tee := livetee.NewWithClose(io.Discard, nil, func() error { close(released); return nil })
	var dst io.Writer
	stream := attachStream(&dst, captured, tee.Writer(w), redactor)
	_, err := dst.Write([]byte(strings.Repeat("safe line\n", 20)))
	s.Require().NoError(err)
	s.wait(w.entered)
	for _, part := range []string{strings.Repeat("x", 2<<20), "privateprefix", "privatesuffix", "\n"} {
		_, err = dst.Write([]byte(part))
		s.Require().NoError(err)
	}
	s.Require().NoError(stream.Flush())
	s.Require().ErrorContains(tee.Finish(), "drain abandoned: true")
	s.NotContains(captured.String(), "privateprefix")
	s.NotContains(captured.String(), "privatesuffix")
	s.True(captured.String() == strings.Repeat("x", subprocess.MaxCapturedBytes-4)+"***\n")
	s.True(captured.Truncated())
	close(w.release)
	s.wait(released)
	s.NotContains(w.String(), "privateprefix")
	s.NotContains(w.String(), "privatesuffix")
}

func (s *liveOutputSuite) TestRedactionAndOrdering() {
	secret := strings.Join([]string{"credential", "value"}, "")
	binary := s.binary(
		"printf 'one\ntwo\nthree\n%s\n' \"$DEVSY_TEST_TOKEN\"\nprintf 'error %s\n' \"$DEVSY_TEST_TOKEN\" >&2\n",
	)
	var out, stderr bytes.Buffer
	result, err := (execRunner{}).Run(
		context.Background(),
		RunOptions{
			Binary: liveTestShell, Args: []string{binary},
			Env:    []string{"DEVSY_TEST_TOKEN=" + secret},
			Stdout: &out,
			Stderr: &stderr,
		},
	)
	s.Require().NoError(err)
	s.Equal("one\ntwo\nthree\n***\n", out.String())
	s.Equal(out.String(), string(result.Stdout))
	s.Equal("error ***\n", stderr.String())
	s.Equal(stderr.String(), string(result.Stderr))
}

func (s *liveOutputSuite) TestCredentialCapture() {
	binary := s.binary("printf 'protocol=https\npassword=%s\n' \"$DEVSY_TEST_TOKEN\"\n")
	secret := strings.Join([]string{"credential", "value"}, "")
	var out bytes.Buffer
	result, err := (execRunner{}).Run(
		context.Background(),
		RunOptions{
			Binary: liveTestShell, Args: []string{binary},
			Env:              []string{"DEVSY_TEST_TOKEN=" + secret},
			Stdout:           &out,
			UnredactedStdout: true,
		},
	)
	s.Require().NoError(err)
	s.Equal("protocol=https\npassword="+secret+"\n", string(result.Stdout))
	s.Equal(string(result.Stdout), out.String())
}

func (s *liveOutputSuite) wait(ch <-chan struct{}) {
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		s.FailNow("channel deadline exceeded")
	}
}

func (s *liveOutputSuite) binary(script string) string {
	if runtime.GOOS == "windows" {
		s.T().Skip("requires a POSIX shell")
	}
	path := filepath.Join(s.T().TempDir(), "fake-git")
	s.Require().NoError(os.WriteFile(path, []byte(script), 0o600))
	return path
}
