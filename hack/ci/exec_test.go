package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

// TestHelperProcess is re-executed by ExecRunner tests; it is not a real test.
func TestHelperProcess(t *testing.T) {
	mode := os.Getenv("DEVSY_CI_HELPER")
	if mode == "" {
		t.Skip("helper process only")
	}
	switch mode {
	case "exit3":
		fmt.Fprint(os.Stderr, "bad things")
		os.Exit(3)
	case "sleep":
		time.Sleep(time.Minute)
	case "flood":
		chunk := strings.Repeat("x", 1024)
		for range 4 * maxCaptureBytes / 1024 {
			fmt.Print(chunk)
		}
		fmt.Print("END")
	case "env":
		fmt.Print(os.Getenv("DEVSY_CI_VAR"))
	}
	os.Exit(0)
}

type ExecSuite struct{ suite.Suite }

func TestExecSuite(t *testing.T) { suite.Run(t, new(ExecSuite)) }

func helperCommand(mode string, extraEnv ...string) Command {
	return Command{
		Path:    os.Args[0],
		Args:    []string{"-test.run=^TestHelperProcess$"},
		Env:     append([]string{"DEVSY_CI_HELPER=" + mode}, extraEnv...),
		Timeout: 30 * time.Second,
	}
}

func (s *ExecSuite) TestExitCodeAndStderr() {
	res, err := ExecRunner{}.Run(context.Background(), helperCommand("exit3"))
	s.Require().NoError(err)
	s.Equal(3, res.ExitCode)
	s.Contains(res.Stderr, "bad things")
	s.False(res.TimedOut)
}

func (s *ExecSuite) TestTimeoutKillsChild() {
	cmd := helperCommand("sleep")
	cmd.Timeout = 300 * time.Millisecond
	start := time.Now()
	res, err := ExecRunner{}.Run(context.Background(), cmd)
	s.Require().NoError(err)
	s.True(res.TimedOut)
	s.Equal(-1, res.ExitCode)
	s.Less(time.Since(start), 20*time.Second)
}

func (s *ExecSuite) TestCaptureIsTailCappedAndLiveTee() {
	var live bytes.Buffer
	cmd := helperCommand("flood")
	cmd.Stdout = &live
	res, err := ExecRunner{}.Run(context.Background(), cmd)
	s.Require().NoError(err)
	s.Zero(res.ExitCode)
	s.LessOrEqual(len(res.Stdout), maxCaptureBytes+64)
	s.Contains(res.Stdout, "earlier bytes dropped")
	s.True(strings.HasSuffix(res.Stdout, "END"))
	s.Greater(live.Len(), maxCaptureBytes, "live writer sees everything")
}

func (s *ExecSuite) TestEnvOverlayReplacesParent() {
	s.T().Setenv("DEVSY_CI_VAR", "parent")
	res, err := ExecRunner{}.Run(context.Background(), helperCommand("env", "DEVSY_CI_VAR=child"))
	s.Require().NoError(err)
	s.Equal("child", strings.TrimSpace(res.Stdout))
}

func (s *ExecSuite) TestStartFailureAndCancel() {
	_, err := ExecRunner{}.Run(context.Background(), Command{Path: "/definitely/not/here"})
	s.Require().Error(err)
	s.Contains(err.Error(), "/definitely/not/here")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := ExecRunner{}.Run(ctx, helperCommand("sleep"))
	s.Require().ErrorIs(err, context.Canceled)
	s.Equal(-1, res.ExitCode)

	_, err = ExecRunner{}.Run(context.Background(), Command{})
	s.Require().Error(err)
}

func (s *ExecSuite) TestTailBuffer() {
	tb := newTailBuffer(8)
	_, _ = tb.Write([]byte("abcd"))
	s.Equal("abcd", tb.String())
	_, _ = tb.Write([]byte("efghij"))
	s.Equal("[2 earlier bytes dropped]\ncdefghij", tb.String())
	_, _ = tb.Write([]byte("0123456789"))
	s.Equal("[12 earlier bytes dropped]\n23456789", tb.String())
}

func (s *ExecSuite) TestOverlayEnv() {
	const (
		empty  = "EMPTY="
		newest = "PATH=/newer"
		home   = "HOME=/h"
	)
	base := []string{"PATH=/a", "Path=/b", home, hiddenDriveEntry, empty}
	overlay := []string{"PATH=/new", envDockerContextEmpty, newest}

	s.Equal(
		[]string{"Path=/b", home, hiddenDriveEntry, empty, envDockerContextEmpty, newest},
		overlayEnv(base, overlay, false),
		"case-sensitive keeps Path distinct",
	)
	s.Equal(
		[]string{home, hiddenDriveEntry, empty, envDockerContextEmpty, newest},
		overlayEnv(base, overlay, true),
		"Windows folds case and drops duplicates",
	)
	s.Equal(base, overlayEnv(base, nil, true))
}

func (s *ExecSuite) TestLookupEnv() {
	env := []string{"Path=C:\\a", "X=1", "X=2", "=C:=C:\\"}
	v, ok := lookupEnv(env, "PATH", true)
	s.True(ok)
	s.Equal("C:\\a", v)
	_, ok = lookupEnv(env, "PATH", false)
	s.False(ok)
	v, _ = lookupEnv(env, "X", false)
	s.Equal("2", v)
	v, ok = lookupEnv(env, "=C:", false)
	s.True(ok)
	s.Equal("C:\\", v)
}

func (s *ExecSuite) TestRedact() {
	fake := "ghs_" + "0123456789abcdefghijklmnopqrstuv"
	pat := "github_pat_" + "11ABCDEFG0123456789_abcdefghijk"
	key := "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXk\nAAAA\n-----END OPENSSH PRIVATE KEY-----"
	for name, tc := range map[string]struct {
		in      string
		secrets []string
		gone    string
	}{
		"github token":      {"token " + fake + " end", nil, fake},
		"fine grained pat":  {"x " + pat, nil, pat},
		"bearer header":     {"Authorization: Bearer abc.def.ghi", nil, "abc.def.ghi"},
		"basic header":      {"authorization: basic dXNlcjpwYXNz", nil, "dXNlcjpwYXNz"},
		"env assignment":    {"GH_ACCESS_TOKEN=hunter22secret next", nil, "hunter22secret"},
		"quoted assignment": {`DB_PASSWORD="two words"`, nil, "two words"},
		"docker config":     {`{"auths":{"r":{"auth": "dXNlcjpwYXNz"}}}`, nil, "dXNlcjpwYXNz"},
		"url userinfo":      {"git clone https://bot:s3cr3tpass@host/x", nil, "s3cr3tpass"},
		"private key":       {"before\n" + key + "\nafter", nil, "b3BlbnNzaC1rZXk"},
		"unterminated key":  {"-----BEGIN RSA PRIVATE KEY-----\nMIIEow", nil, "MIIEow"},
		"exact secret":      {"value is opaque-value-123 here", []string{"opaque-value-123"}, "opaque-value-123"},
	} {
		s.Run(name, func() {
			got := redact(tc.in, tc.secrets...)
			s.NotContains(got, tc.gone)
			s.Contains(got, redacted)
		})
	}
	s.Equal("plain output: 3 specs passed", redact("plain output: 3 specs passed"))
	s.Equal("short x stays", redact("short x stays", "x"), "tiny secrets ignored")
}
