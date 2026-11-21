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

const (
	captureChildArg = "-test.run=^TestCaptureChild$"
	capturePrefix   = "Authorization:\n Bearer "
	captureSuffix   = "\npublic capture detail\n"
)

func TestCaptureChild(t *testing.T) {
	switch os.Getenv("DEVSY_CAPTURE_CHILD") {
	case "exit":
		_, _ = fmt.Fprint(os.Stdout, "stdout")
		_, _ = fmt.Fprint(os.Stderr, "stderr")
		os.Exit(3)
	case "folded":
		_, _ = fmt.Fprint(os.Stdout, capturePrefix)
		_, _ = fmt.Fprint(os.Stdout, "framework-stdout-fixture"+captureSuffix)
		_, _ = fmt.Fprint(os.Stderr, capturePrefix)
		_, _ = fmt.Fprint(os.Stderr, "framework-stderr-fixture"+captureSuffix)
		os.Exit(0)
	case "pretty-json":
		const record = "[INFO] start\n{\n  \"Cookie\": \"framework-cookie-fixture\",\n" +
			"  \"note\": \"public json detail\"\n}\npublic json tail\n"
		_, _ = fmt.Fprint(os.Stdout, record)
		_, _ = fmt.Fprint(os.Stderr, record)
		os.Exit(0)
	case "open-json":
		const record = "{\n  \"Cookie\": \"framework-open-fixture\"\n"
		_, _ = fmt.Fprint(os.Stdout, record)
		_, _ = fmt.Fprint(os.Stderr, record)
		os.Exit(0)
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
		ExecCommandCapture(context.Background(), []string{captureChildArg})
	var exit *exec.ExitError
	s.Require().ErrorAs(err, &exit)
	s.Equal(3, exit.ExitCode())
	s.Equal("stdout", out)
	s.Equal("stderr", stderr)
}

func (s *captureSuite) TestCaptureMasksOnlyLiveDiagnosticCopies() {
	s.T().Setenv("DEVSY_CAPTURE_CHILD", "folded")
	outRead, outWrite, err := os.Pipe()
	s.Require().NoError(err)
	defer func() { _ = outRead.Close() }()
	defer func() { _ = outWrite.Close() }()
	errRead, errWrite, err := os.Pipe()
	s.Require().NoError(err)
	defer func() { _ = errRead.Close() }()
	defer func() { _ = errWrite.Close() }()
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outWrite, errWrite
	defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()

	out, stderr, err := s.framework().
		ExecCommandDiagnosticCapture(s.T().Context(), []string{captureChildArg})
	s.Require().NoError(err)
	s.Equal(capturePrefix+"framework-stdout-fixture"+captureSuffix, out)
	s.Equal(capturePrefix+"framework-stderr-fixture"+captureSuffix, stderr)
	s.Require().NoError(outWrite.Close())
	s.Require().NoError(errWrite.Close())
	liveOut, err := io.ReadAll(outRead)
	s.Require().NoError(err)
	liveErr, err := io.ReadAll(errRead)
	s.Require().NoError(err)
	for _, value := range [][]byte{liveOut, liveErr} {
		s.Contains(string(value), "public capture detail")
		s.NotContains(string(value), "framework-stdout-fixture")
		s.NotContains(string(value), "framework-stderr-fixture")
		s.Contains(string(value), "***")
	}
}

func (s *captureSuite) TestLiveDiagnosticsMaskMultilineJSONCredentials() {
	got := s.liveDiagnostics("pretty-json")
	s.Contains(got.out, "framework-cookie-fixture")
	s.Contains(got.stderr, "framework-cookie-fixture")
	for _, live := range []string{got.liveOut, got.liveErr} {
		s.NotContains(live, "framework-cookie-fixture")
		s.Contains(live, "[INFO] start\n")
		s.Contains(live, "public json detail")
		s.Contains(live, "public json tail\n")
	}
}

func (s *captureSuite) TestLiveDiagnosticsMaskUnterminatedJSONRecord() {
	got := s.liveDiagnostics("open-json")
	s.Contains(got.out, "framework-open-fixture")
	for _, live := range []string{got.liveOut, got.liveErr} {
		s.NotContains(live, "framework-open-fixture")
		s.Contains(live, "***")
	}
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
		out, stderr, err := f.ExecCommandCapture(ctx, []string{captureChildArg})
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

type liveCapture struct{ out, stderr, liveOut, liveErr string }

func (s *captureSuite) liveDiagnostics(mode string) liveCapture {
	s.T().Setenv("DEVSY_CAPTURE_CHILD", mode)
	outRead, outWrite, err := os.Pipe()
	s.Require().NoError(err)
	defer func() { _ = outRead.Close() }()
	defer func() { _ = outWrite.Close() }()
	errRead, errWrite, err := os.Pipe()
	s.Require().NoError(err)
	defer func() { _ = errRead.Close() }()
	defer func() { _ = errWrite.Close() }()
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outWrite, errWrite
	defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()

	out, stderr, err := s.framework().
		ExecCommandDiagnosticCapture(s.T().Context(), []string{captureChildArg})
	s.Require().NoError(err)
	s.Require().NoError(outWrite.Close())
	s.Require().NoError(errWrite.Close())
	liveOut, err := io.ReadAll(outRead)
	s.Require().NoError(err)
	liveErr, err := io.ReadAll(errRead)
	s.Require().NoError(err)
	return liveCapture{out, stderr, string(liveOut), string(liveErr)}
}

func (s *captureSuite) framework() *Framework {
	executable, err := os.Executable()
	s.Require().NoError(err)
	return &Framework{
		DevsyBinDir:  filepath.Dir(executable),
		DevsyBinName: filepath.Base(executable),
	}
}
