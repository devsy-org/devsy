package framework

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

func TestCaptureChild(t *testing.T) {
	switch os.Getenv("DEVSY_CAPTURE_CHILD") {
	case "exit":
		_, _ = fmt.Fprint(os.Stdout, "stdout")
		_, _ = fmt.Fprint(os.Stderr, "stderr")
		os.Exit(3)
	case "blocked":
		_, _ = fmt.Fprint(os.Stdout, strings.Repeat("x", 2<<20))
		_, _ = fmt.Fprint(os.Stderr, "ready")
		time.Sleep(time.Minute)
		os.Exit(0)
	}
}

type captureSuite struct{ suite.Suite }

func TestCaptureSuite(t *testing.T) { suite.Run(t, new(captureSuite)) }
func (s *captureSuite) TestExit() {
	s.T().Setenv("DEVSY_CAPTURE_CHILD", "exit")
	out, stderr, err := s.framework().
		ExecCommandCapture(context.Background(), []string{"-test.run=^TestCaptureChild$"})
	var exit *exec.ExitError
	s.Require().ErrorAs(err, &exit)
	s.Equal(3, exit.ExitCode())
	s.Equal("stdout", out)
	s.Equal("stderr", stderr)
}

func (s *captureSuite) TestBlockedLiveOutput() {
	s.T().Setenv("DEVSY_CAPTURE_CHILD", "blocked")
	outRead, outWrite, err := os.Pipe()
	s.Require().NoError(err)
	errRead, errWrite, err := os.Pipe()
	s.Require().NoError(err)
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outWrite, errWrite
	defer func() {
		os.Stdout, os.Stderr = oldOut, oldErr
		_ = outRead.Close()
		_ = outWrite.Close()
		_ = errRead.Close()
		_ = errWrite.Close()
	}()
	f := s.framework()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		out, stderr string
		err         error
	}
	done := make(chan result, 1)
	go func() {
		out, stderr, err := f.ExecCommandCapture(ctx, []string{"-test.run=^TestCaptureChild$"})
		done <- result{out, stderr, err}
	}()
	ready := make(chan error, 1)
	go func() {
		b := make([]byte, 5)
		_, err := io.ReadFull(errRead, b)
		if err == nil && string(b) != "ready" {
			err = fmt.Errorf("unexpected stderr %q", b)
		}
		ready <- err
	}()
	select {
	case err := <-ready:
		s.Require().NoError(err)
	case <-time.After(15 * time.Second):
		s.FailNow("child did not complete output")
	}
	cancel()
	select {
	case got := <-done:
		s.ErrorIs(got.err, context.Canceled)
		s.Equal(strings.Repeat("x", 2<<20), got.out)
		s.Equal("ready", got.stderr)
		dropped, _ := f.LiveOutputDropped()
		s.Positive(dropped)
	case <-time.After(40 * time.Second):
		s.FailNow("capture did not return")
	}
}

func (s *captureSuite) framework() *Framework {
	executable, err := os.Executable()
	s.Require().NoError(err)
	return &Framework{
		DevsyBinDir:  filepath.Dir(executable),
		DevsyBinName: filepath.Base(executable),
	}
}
