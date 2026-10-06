package external

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing/iotest"
	"time"

	"github.com/devsy-org/devsy-runtime-sdk/conformance/fake"
	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"github.com/devsy-org/devsy/pkg/driver"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	earlyExitCommand = "early-exit"
	echoCommand      = "echo"
)

func (s *HostSuite) TestExecLargeDuplexBinaryStream() {
	host := s.runningHost(fake.Conformance)
	// Larger than the gRPC flow-control window; NUL and invalid UTF-8 stay intact.
	data := bytes.Repeat([]byte{0, 255, 'a', '\n', 128}, 1<<20)
	var stdout, stderr bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := host.CommandContainerArgv(ctx, fixtureWorkspace, []string{"duplex"}, driver.Streams{
		Stdin: bytes.NewReader(data), Stdout: &stdout, Stderr: &stderr,
	})
	s.Require().NoError(err)
	s.Equal(data, stdout.Bytes())
	s.Equal(data, stderr.Bytes())
}

func (s *HostSuite) TestExecExactArgvAndShellMetadata() {
	host := s.host("stream-metadata")
	argv := []string{"space name", "", "é", "$(touch nope)", "quote'\""}
	var stdout bytes.Buffer
	s.Require().NoError(host.CommandContainerArgv(context.Background(), fixtureWorkspace, argv,
		driver.Streams{Stdout: &stdout}))
	var start runtimev1.ExecStart
	s.Require().NoError(json.Unmarshal(stdout.Bytes(), &start))
	s.Equal(argv, start.Argv)
	s.Equal("root", start.User)
	s.Equal(fixtureWorkspace, start.WorkspaceId)
	s.False(start.Tty)
	stdout.Reset()
	command := "printf '%s' '$HOME; quoted'"
	s.Require().NoError(host.CommandDevContainer(context.Background(), &driver.CommandParams{
		WorkspaceID: fixtureWorkspace, User: "guest", Command: command, Stdout: &stdout,
	}))
	s.Require().NoError(json.Unmarshal(stdout.Bytes(), &start))
	s.Equal([]string{"/bin/sh", "-c", command}, start.Argv)
	s.Equal("guest", start.User)
}

func (s *HostSuite) TestExecTextRedactionAndRawStdout() {
	s.T().Setenv("FIXTURE_TOKEN", "private-canary-value")
	host := s.host("stream-secret")
	for _, raw := range []bool{false, true} {
		var stdout, stderr bytes.Buffer
		s.Require().NoError(host.CommandDevContainer(context.Background(), &driver.CommandParams{
			WorkspaceID: fixtureWorkspace, Command: fixtureImage, RawStdout: raw,
			Stdout: &stdout, Stderr: &stderr,
		}))
		s.Equal(raw, strings.Contains(stdout.String(), "private-canary-value"))
		s.NotContains(stderr.String(), "private-canary-value")
		s.Contains(stdout.String(), "safe-tail")
		s.Contains(stderr.String(), "safe-tail")
	}
}

func (s *HostSuite) TestExecEmptyInputAndExitOutcomes() {
	host := s.runningHost(fake.Conformance)
	for _, command := range []string{echoCommand, earlyExitCommand, "nonzero", "signal"} {
		s.Run(command, func() {
			err := host.CommandContainerArgv(context.Background(), fixtureWorkspace,
				[]string{command}, driver.Streams{})
			if command == echoCommand || command == earlyExitCommand {
				s.NoError(err)
				return
			}
			var exit *CommandExitError
			s.Require().ErrorAs(err, &exit)
			if command == "signal" {
				s.Equal("TERM", exit.Signal)
				s.Zero(exit.ExitCode())
			} else {
				s.Equal(7, exit.ExitCode())
			}
		})
	}
	err := host.CommandContainerArgv(context.Background(), fixtureWorkspace,
		[]string{"error", "PERMISSION_DENIED"}, driver.Streams{})
	s.Equal(codes.PermissionDenied, status.Code(err))
	var failure *RuntimeError
	s.Require().ErrorAs(err, &failure)
	s.Equal(runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_PERMISSION_DENIED, failure.Category)
}

func (s *HostSuite) TestExecRejectsMalformedCompletion() {
	for mode, message := range map[string]string{
		"stream-missing-exit":   "without a terminal exit",
		"stream-duplicate-exit": "after its terminal exit",
		"stream-after-exit":     "after its terminal exit",
		"stream-unset-frame":    "invalid output frame",
		"stream-empty-chunk":    "1..32768 bytes",
		"stream-large-chunk":    "1..32768 bytes",
		"stream-exit-rpc-error": "failure after exit",
	} {
		s.Run(mode, func() {
			host := s.host(mode)
			err := host.CommandContainerArgv(context.Background(), fixtureWorkspace,
				[]string{fixtureImage}, driver.Streams{})
			s.ErrorContains(err, message)
		})
	}
}

func (s *HostSuite) TestExecCancellationClosesInputAndReapsChild() {
	host := s.host("stream-block-child")
	input, producer := io.Pipe()
	defer func() { _ = producer.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- host.CommandContainerArgv(ctx, fixtureWorkspace,
			[]string{fixtureImage}, driver.Streams{Stdin: input})
	}()
	s.waitForStream(host)
	cancel()
	select {
	case err := <-done:
		s.ErrorIs(err, context.Canceled)
	case <-time.After(5 * time.Second):
		s.FailNow("Exec cancellation did not join its input pump")
	}
	_, err := producer.Write([]byte("late"))
	s.ErrorIs(err, io.ErrClosedPipe)
}

func (s *HostSuite) TestExecEarlyExitClosesBlockedInput() {
	host := s.runningHost(fake.Conformance)
	input, producer := io.Pipe()
	defer func() { _ = producer.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.NoError(host.CommandContainerArgv(ctx, fixtureWorkspace,
		[]string{earlyExitCommand}, driver.Streams{Stdin: input}))
	_, err := producer.Write([]byte("late"))
	s.ErrorIs(err, io.ErrClosedPipe)
}

func (s *HostSuite) TestExecEarlyExitClosesFileInput() {
	host := s.runningHost(fake.Conformance)
	input, producer, err := os.Pipe()
	s.Require().NoError(err)
	defer func() { _ = input.Close(); _ = producer.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.NoError(host.CommandContainerArgv(ctx, fixtureWorkspace,
		[]string{earlyExitCommand}, driver.Streams{Stdin: input}))
	_, err = input.Stat()
	s.ErrorIs(err, os.ErrClosed)
}

func (s *HostSuite) TestExecDeadlineAndCrash() {
	for _, mode := range []string{fake.ExecSlow, fake.ExecCrash} {
		s.Run(mode, func() {
			host := s.runningHost(mode)
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			err := host.CommandContainerArgv(
				ctx,
				fixtureWorkspace,
				[]string{fixtureImage},
				driver.Streams{},
			)
			if mode == fake.ExecSlow {
				s.ErrorIs(err, context.DeadlineExceeded)
			} else {
				s.Error(err)
			}
		})
	}
}

func (s *HostSuite) TestExecInputAndOutputFailures() {
	host := s.runningHost(fake.Conformance)
	for _, streams := range []driver.Streams{
		{Stdin: iotest.ErrReader(errors.New("input failed"))},
		{Stdin: strings.NewReader("output"), Stdout: shortWriter{}},
		{Stdin: strings.NewReader("output"), Stderr: shortWriter{}},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := host.CommandContainerArgv(ctx, fixtureWorkspace, []string{"duplex"}, streams)
		cancel()
		s.Error(err)
		s.NotErrorIs(err, context.DeadlineExceeded)
	}
}

func (s *HostSuite) TestExecReaderBoundarySemantics() {
	host := s.runningHost(fake.Conformance)
	data := []byte("final bytes\x00\xff")
	for _, input := range []io.Reader{
		iotest.DataErrReader(bytes.NewReader(data)),
		&emptyReadsReader{Reader: bytes.NewReader(data), remaining: 3},
	} {
		var stdout bytes.Buffer
		s.Require().NoError(host.CommandContainerArgv(context.Background(), fixtureWorkspace,
			[]string{echoCommand}, driver.Streams{Stdin: input, Stdout: &stdout}))
		s.Equal(data, stdout.Bytes())
	}
	err := host.CommandContainerArgv(context.Background(), fixtureWorkspace,
		[]string{echoCommand}, driver.Streams{Stdin: &emptyReadsReader{remaining: 100}})
	s.ErrorContains(err, io.ErrNoProgress.Error())
}

func (s *HostSuite) TestExecValidatesBeforeLaunch() {
	host := s.host(fake.Normal)
	host.config.Binaries[fixtureKey][0].Checksum = "bad"
	s.ErrorContains(host.CommandDevContainer(context.Background(), nil), "parameters")
	for _, argv := range [][]string{nil, {}, {""}} {
		s.ErrorContains(host.CommandContainerArgv(context.Background(), fixtureWorkspace,
			argv, driver.Streams{}), "nonempty executable")
	}
	s.ErrorContains(host.CommandContainerArgv(context.Background(), "",
		[]string{"exec"}, driver.Streams{}), "workspace ID")
}

func (s *HostSuite) runningHost(mode string) *Host {
	host := s.host(mode)
	s.Require().NoError(host.RunImage(context.Background(), &runtimev1.RunImageRequest{
		WorkspaceId: fixtureWorkspace, Image: fixtureImage,
	}))
	return host
}

func (s *HostSuite) waitForStream(host *Host) {
	directory := host.config.External.Args[3]
	s.Require().Eventually(func() bool {
		_, err := os.Stat(filepath.Join(directory, "ready"))
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)
}

type shortWriter struct{}

func (shortWriter) Write(data []byte) (int, error) { return len(data) - 1, nil }

type emptyReadsReader struct {
	io.Reader
	remaining int
}

func (r *emptyReadsReader) Read(data []byte) (int, error) {
	if r.remaining > 0 {
		r.remaining--
		return 0, nil
	}
	return r.Reader.Read(data)
}
