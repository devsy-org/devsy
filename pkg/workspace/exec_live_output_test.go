package workspace

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"testing"

	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/stretchr/testify/suite"
)

type execLiveOutputSuite struct{ suite.Suite }

func TestExecLiveOutputSuite(t *testing.T) { suite.Run(t, new(execLiveOutputSuite)) }

func (s *execLiveOutputSuite) TestExitPreservesOutputError() {
	executable, err := os.Executable()
	s.Require().NoError(err)
	process, err := os.StartProcess(
		executable,
		[]string{executable, "-test.run=^TestExecLiveOutputChild$"},
		&os.ProcAttr{
			Env:   append(os.Environ(), "DEVSY_TEST_EXIT_CHILD=1"),
			Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
		},
	)
	s.Require().NoError(err)
	state, err := process.Wait()
	s.Require().NoError(err)
	exitErr := &exec.ExitError{ProcessState: state}
	exit := error(exitErr)
	outputErr := &docker.LiveOutputError{Err: errors.New("live output: 12 bytes dropped")}
	run := func(context.Context, []string, io.Reader, io.Writer, io.Writer) error {
		return errors.Join(exit, outputErr)
	}
	code, err := execWithRunner(context.Background(), ExecRequest{}, run)
	s.Equal(exitErr.ExitCode(), code)
	s.ErrorIs(err, outputErr)
	s.ErrorContains(err, "12 bytes dropped")
}

func TestExecLiveOutputChild(t *testing.T) {
	if os.Getenv("DEVSY_TEST_EXIT_CHILD") == "1" {
		os.Exit(7)
	}
}

func (s *execLiveOutputSuite) TestSuccessfulExitPreservesOutputError() {
	outputErr := &docker.LiveOutputError{Err: errors.New("live output: 12 bytes dropped")}
	run := func(context.Context, []string, io.Reader, io.Writer, io.Writer) error { return outputErr }
	code, err := execWithRunner(context.Background(), ExecRequest{}, run)
	s.Zero(code)
	s.ErrorIs(err, outputErr)
}

func (s *execLiveOutputSuite) TestMachineryErrorWithOutputError() {
	outputErr := &docker.LiveOutputError{Err: errors.New("live output: 12 bytes dropped")}
	commandErr := errors.New("copy failed")
	run := func(context.Context, []string, io.Reader, io.Writer, io.Writer) error {
		return errors.Join(commandErr, outputErr)
	}
	code, err := execWithRunner(context.Background(), ExecRequest{}, run)
	s.Equal(-1, code)
	s.ErrorIs(err, commandErr)
	s.ErrorIs(err, outputErr)
}

func TestMain(m *testing.M) {
	if os.Getenv("DEVSY_TEST_DOCKER_HELPER") == "1" {
		_, _ = io.WriteString(os.Stdout, "secretprefixsecretsuffix")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func (s *execLiveOutputSuite) TestRequestEnvironmentRedactsLiveOutput() {
	executable, err := os.Executable()
	s.Require().NoError(err)
	runtime := &DockerRuntime{helper: &docker.DockerHelper{
		DockerCommand: executable,
		Environment:   []string{"DEVSY_TEST_DOCKER_HELPER=1"},
	}}
	var live bytes.Buffer
	code, err := runtime.Exec(context.Background(), ExecRequest{
		Target: ContainerTarget{ContainerID: "test"},
		Env:    map[string]string{"API_TOKEN": "secretprefixsecretsuffix"},
		Stdout: &live, LiveStdout: true,
	})
	s.Require().NoError(err)
	s.Zero(code)
	s.NotContains(live.String(), "secretprefixsecretsuffix")
	s.Contains(live.String(), "***")
}
