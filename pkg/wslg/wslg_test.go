package wslg

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

const (
	testDistro          = "Ubuntu"
	testUser            = "alice"
	testOrdinaryDisplay = "DISPLAY=:9"
	testProcessHelper   = "-test.run=^TestWSLgProcessHelper$"
)

type WSLgSuite struct{ suite.Suite }

func TestWSLgSuite(t *testing.T) { suite.Run(t, new(WSLgSuite)) }

type fakeRunner struct {
	run func(context.Context, string, []string) (ProbeResult, error)
}

func (r fakeRunner) Run(ctx context.Context, path string, args []string) (ProbeResult, error) {
	return r.run(ctx, path, args)
}

func (s *WSLgSuite) TestCaptureAllowlistPresenceAndImmutability() {
	calls := 0
	target := Target{Distro: "Ubuntu-24.04", User: testUser}
	snapshot, err := Capture(
		context.Background(),
		target,
		"trusted-wsl",
		fakeRunner{run: func(_ context.Context, path string, args []string) (ProbeResult, error) {
			s.Equal("trusted-wsl", path)
			names := []string{"DISPLAY", "WAYLAND_DISPLAY", "XDG_RUNTIME_DIR"}
			s.Equal(
				[]string{
					"--distribution",
					target.Distro,
					"--user",
					target.User,
					"--exec",
					"/usr/bin/printenv",
					names[calls],
				},
				args,
			)
			results := []ProbeResult{
				{Stdout: []byte(":0\r\n")},
				{Stdout: []byte("\n")},
				{ExitCode: 1},
			}
			result := results[calls]
			calls++
			return result, nil
		}},
	)
	s.Require().NoError(err)
	s.Equal(3, calls)
	s.Equal(target, snapshot.Target())
	s.Equal(map[string]string{Display: ":0", WaylandDisplay: ""}, snapshot.Environment())
	env := snapshot.Environment()
	env[Display] = "changed"
	s.Equal(":0", snapshot.Environment()[Display])
}

func (s *WSLgSuite) TestEmptySnapshotEnvironmentIsNonNil() {
	snapshot, err := NewSnapshot(Target{Distro: testDistro, User: testUser}, nil)
	s.Require().NoError(err)
	s.NotNil(snapshot.Environment())
	s.Empty(snapshot.Environment())
	s.Error(ValidateEnvironment(map[string]string{Display: "a\x1bb"}))
}

func (s *WSLgSuite) TestProbeEnvironmentDropsWSLENV() {
	env := withoutWSLENV([]string{"A=1", "WSLENV=DISPLAY/u", "wslenv=x", "DISPLAY=:3"})
	s.Equal([]string{"A=1", "DISPLAY=:3"}, env)
}

func (s *WSLgSuite) TestInvalidTargetsNeverRun() {
	for _, target := range []Target{
		{},
		{"-Ubuntu", testUser},
		{testDistro, "root"},
		{testDistro, "0"},
		{"Ubuntu x", testUser},
		{testDistro, "--help"},
		{testDistro, "alice\n"},
		{testDistro, strings.Repeat("a", 257)},
	} {
		_, err := Capture(
			context.Background(),
			target,
			"wsl",
			fakeRunner{run: func(context.Context, string, []string) (ProbeResult, error) {
				s.Fail("must not run")
				return ProbeResult{}, nil
			}},
		)
		s.ErrorIs(err, ErrInvalidTarget)
	}
}

func (s *WSLgSuite) TestStrictProtocol() {
	for _, result := range []ProbeResult{
		{Stdout: []byte("private")},
		{Stdout: []byte("private\n\n")},
		{Stdout: []byte("a\x00b\n")},
		{Stdout: []byte{0xff, '\n'}},
		{Stdout: []byte("a\rb\n")},
		{Stdout: []byte("a\tb\n")},
		{Stdout: []byte("a\x1bb\n")},
		{Stdout: []byte("a\x7fb\n")},
		{Stdout: []byte("a\n"), Stderr: []byte("private")},
		{ExitCode: 1, Stdout: []byte("private\n")},
		{ExitCode: 2},
		{Stdout: []byte(strings.Repeat("a", outputLimit) + "\n")},
	} {
		_, _, err := parseResult(result)
		s.ErrorIs(err, ErrProtocol)
		s.NotContains(err.Error(), "private")
	}
	for _, output := range []string{"\n", "\r\n", "a$${literal}\n"} {
		_, present, err := parseResult(ProbeResult{Stdout: []byte(output)})
		s.NoError(err)
		s.True(present)
	}
	_, present, err := parseResult(ProbeResult{ExitCode: 1})
	s.NoError(err)
	s.False(present)
}

func (s *WSLgSuite) TestCancellationAndUnavailable() {
	target := Target{testDistro, testUser}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Capture(ctx, target, "wsl", fakeRunner{})
	s.ErrorIs(err, context.Canceled)
	for _, result := range []ProbeResult{{ExitCode: 1}, {Stdout: []byte("\n")}} {
		_, err = Capture(
			context.Background(),
			target,
			"wsl",
			fakeRunner{
				run: func(context.Context, string, []string) (ProbeResult, error) { return result, nil },
			},
		)
		s.ErrorIs(err, ErrUnavailable)
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	_, err = Capture(
		ctx,
		target,
		"wsl",
		fakeRunner{run: func(ctx context.Context, _ string, _ []string) (ProbeResult, error) {
			<-ctx.Done()
			return ProbeResult{}, ctx.Err()
		}},
	)
	s.ErrorIs(err, context.DeadlineExceeded)
}

func (s *WSLgSuite) TestNamespaceAndOverlay() {
	s.ErrorIs(CheckCollisions("process", []string{"devsy_wslg_display="}, true), ErrCollision)
	s.NoError(CheckCollisions("process", []string{"devsy_wslg_display="}, false))
	s.NoError(CheckCollisions("process", []string{testOrdinaryDisplay}, true))
	env := Overlay(
		[]string{testOrdinaryDisplay, "devsy_wslg_display=old", Display + "=duplicate"},
		map[string]string{Display: ":0", RuntimeDir: ""},
		true,
	)
	s.Equal([]string{testOrdinaryDisplay, Display + "=:0", RuntimeDir + "="}, env)
	s.Error(ValidateEnvironment(map[string]string{"PULSE_SERVER": "secret"}))
	s.Error(ValidateEnvironment(map[string]string{Display: "x\n"}))
}

func (s *WSLgSuite) TestSubprocessBoundsAndCancellation() {
	path, err := os.Executable()
	s.Require().NoError(err)
	result, err := (commandRunner{}).Run(
		context.Background(),
		path,
		[]string{testProcessHelper, "--", "large"},
	)
	s.ErrorIs(err, ErrProtocol)
	s.Empty(result.Stdout)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = (commandRunner{}).Run(
		ctx,
		path,
		[]string{testProcessHelper, "--", "wait"},
	)
	s.ErrorIs(err, context.DeadlineExceeded)
	result, err = (commandRunner{}).Run(
		context.Background(),
		path,
		[]string{testProcessHelper, "--", "missing"},
	)
	s.NoError(err)
	s.Equal(1, result.ExitCode)
	s.Empty(result.Stdout)
}

func TestWSLgProcessHelper(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-2] != "--" {
		return
	}
	switch os.Args[len(os.Args)-1] {
	case "large":
		_, err := io.WriteString(os.Stdout, strings.Repeat("a", outputLimit+1))
		if err != nil {
			os.Exit(2)
		}
	case "wait":
		time.Sleep(time.Minute)
	case "missing":
		os.Exit(1)
	default:
		t.Fatal(errors.New("invalid helper mode"))
	}
	os.Exit(0)
}
