//go:build linux

package setup

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/pty/ptytest"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/devsy-org/devsy/pkg/workspacelogs"
	"github.com/stretchr/testify/suite"
)

const testTempDir = "/tmp"

type terminalTestSuite struct {
	suite.Suite
	username string
}

type testTerminal struct {
	input          *io.PipeReader
	writer         *io.PipeWriter
	output         lockedOutput
	window         tunnel.TerminalWindow
	resizes        chan tunnel.TerminalWindow
	canceled       chan struct{}
	exit           tunnel.TerminalExit
	exitErr        error
	exitOutput     string
	outputOverride io.Writer
	invocations    []tunnel.TerminalInvocation
}

type lockedOutput struct {
	mu      sync.Mutex
	bytes   bytes.Buffer
	changed chan struct{}
}

func (b *lockedOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n, err := b.bytes.Write(p)
	select {
	case b.changed <- struct{}{}:
	default:
	}
	return n, err
}
func (b *lockedOutput) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.bytes.String() }
func (b *lockedOutput) wait(t *testing.T, value string) {
	t.Helper()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for !strings.Contains(b.String(), value) {
		select {
		case <-b.changed:
		case <-timer.C:
			t.Fatalf("output %q did not contain %q", b.String(), value)
		}
	}
}

func newTestTerminal() *testTerminal {
	r, w := io.Pipe()
	return &testTerminal{
		input:    r,
		writer:   w,
		output:   lockedOutput{changed: make(chan struct{}, 1)},
		window:   tunnel.TerminalWindow{Term: "xterm", Rows: 31, Columns: 91},
		resizes:  make(chan tunnel.TerminalWindow, 1),
		canceled: make(chan struct{}),
	}
}

func (a *testTerminal) Begin(
	_ context.Context,
	inv tunnel.TerminalInvocation,
) (tunnel.TerminalAttachment, error) {
	a.invocations = append(a.invocations, inv)
	return a, nil
}
func (*testTerminal) Close(context.Context) error     { return nil }
func (a *testTerminal) Window() tunnel.TerminalWindow { return a.window }
func (a *testTerminal) Input() io.Reader              { return a.input }
func (a *testTerminal) Output() io.Writer {
	if a.outputOverride != nil {
		return a.outputOverride
	}
	return &a.output
}
func (a *testTerminal) Resizes() <-chan tunnel.TerminalWindow { return a.resizes }
func (a *testTerminal) Canceled() <-chan struct{}             { return a.canceled }
func (a *testTerminal) Exit(_ context.Context, exit tunnel.TerminalExit) error {
	a.exit = exit
	a.exitOutput = a.output.String()
	_ = a.writer.Close()
	return a.exitErr
}

func TestTerminalTestSuite(t *testing.T) { suite.Run(t, new(terminalTestSuite)) }
func (s *terminalTestSuite) SetupTest() {
	u, err := user.Current()
	s.Require().NoError(err)
	s.username = u.Username
	dir := s.T().TempDir()
	script := fmt.Sprintf(
		"#!/bin/sh\nprintf '%%s\\n' '%s:x:%s:%s::%s:/bin/sh'\n",
		u.Username,
		u.Uid,
		u.Gid,
		u.HomeDir,
	)
	s.Require().NoError(os.WriteFile(filepath.Join(dir, "getent"), []byte(script), 0o600))
	s.Require().NoError(os.Chmod(filepath.Join(dir, "getent"), executableTestMode()))
	s.T().Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func (s *terminalTestSuite) TestPromptTTYAndMarker() {
	a := newTestTerminal()
	p := s.params(
		context.Background(),
		a,
		`test -t 0 && test -t 1 && printf 'Name:' && read name && printf '%s' "$name" > marker`,
	)
	outer := ptytest.New(s.T())
	a.outputOverride = outer.Output().Writer
	done := s.run(p)
	outer.ExpectMatchContext(s.T().Context(), "Name:")
	_, err := a.writer.Write([]byte("alice\n"))
	s.Require().NoError(err)
	s.Require().NoError(s.result(done))
	data, err := os.ReadFile(filepath.Join(p.env.workspaceFolder, "marker"))
	s.Require().NoError(err)
	s.Equal("alice", string(data))
	s.Len(a.invocations, 1)
}

func (s *terminalTestSuite) TestInitialSizeAndResize() {
	a := newTestTerminal()
	p := s.params(context.Background(), a, `stty size; printf 'ready:'; read x; stty size`)
	done := s.run(p)
	a.output.wait(s.T(), "ready:")
	s.Contains(a.output.String(), "31 91")
	a.resizes <- tunnel.TerminalWindow{Rows: 42, Columns: 112}
	time.Sleep(100 * time.Millisecond)
	_, err := a.writer.Write([]byte("x\n"))
	s.Require().NoError(err)
	s.Require().NoError(s.result(done))
	s.Contains(a.output.String(), "42 112")
}

func (s *terminalTestSuite) TestExitAndNoRetainedToken() {
	run := newRecordedRun(s.T())
	a := newTestTerminal()
	a.exitErr = fmt.Errorf("exit ack failed")
	p := s.params(run.ctx, a, `printf 'Token:'; read token; exit 7`)
	done := s.run(p)
	a.output.wait(s.T(), "Token:")
	_, err := a.writer.Write([]byte("typed-secret-3187\n"))
	s.Require().NoError(err)
	err = s.result(done)
	s.ErrorContains(err, "exit code 7")
	s.NotContains(err.Error(), "typed-secret-3187")
	s.Equal(7, a.exit.ExitCode)
	records := run.records(s.T())
	s.NotContains(fmt.Sprint(records), "typed-secret-3187")
	got := byKey(s.T(), records, string(PhasePostStart))
	s.Len(got, 1)
	for _, inv := range got {
		s.Equal(workspacelogs.InvocationFailed, inv.final())
		s.Require().NotNil(inv.exitCode)
		s.Equal(7, *inv.exitCode)
		s.Empty(inv.stdout)
		s.Empty(inv.stderr)
	}
}

func (s *terminalTestSuite) TestSignalAndCancel() {
	for _, tc := range []struct {
		name, command string
		cancel        bool
	}{{"signal", `kill -TERM $$`, false}, {"cancel", `printf 'ready:'; while :; do sleep 1; done`, true}} {
		s.Run(tc.name, func() {
			run := newRecordedRun(s.T())
			a := newTestTerminal()
			p := s.params(run.ctx, a, tc.command)
			done := s.run(p)
			if tc.cancel {
				a.output.wait(s.T(), "ready:")
				close(a.canceled)
			}
			s.Error(s.result(done))
			records := run.records(s.T())
			got := byKey(s.T(), records, string(PhasePostStart))
			s.Len(got, 1)
			for _, inv := range got {
				if tc.cancel {
					s.Equal(workspacelogs.InvocationCanceled, inv.final())
				} else {
					s.Equal(workspacelogs.InvocationFailed, inv.final())
					s.Equal(reasonSignaled, inv.reason)
				}
			}
			s.NotEmpty(a.exit.Signal)
		})
	}
}

func (s *terminalTestSuite) TestTermGraceAndKill() {
	a := newTestTerminal()
	p := s.params(
		context.Background(),
		a,
		`trap 'echo trapped' TERM; printf 'ready:'; while :; do :; done`,
	)
	done := s.run(p)
	a.output.wait(s.T(), "ready:")
	started := time.Now()
	close(a.canceled)
	s.Error(s.result(done))
	s.GreaterOrEqual(time.Since(started), 1900*time.Millisecond)
	s.Contains(a.output.String(), "trapped")
	s.NotEmpty(a.exit.Signal)
}

func (s *terminalTestSuite) TestRemoteUserTTYReopen() {
	if os.Geteuid() != 0 {
		s.T().Skip("requires root")
	}
	target, err := user.Lookup("nobody")
	if err != nil {
		s.T().Skip("nobody unavailable")
	}
	if target.Uid == "0" {
		s.T().Skip("no unprivileged account")
	}
	a := newTestTerminal()
	p := s.params(context.Background(), a, `test -w "$(tty)" && (echo reopened > "$(tty)")`)
	p.env.remoteUser = target.Username
	p.env.workspaceFolder = testTempDir
	dir := s.T().TempDir()
	script := fmt.Sprintf(
		"#!/bin/sh\nprintf '%%s\\n' '%s:x:%s:%s::%s:/bin/sh'\n",
		target.Username,
		target.Uid,
		target.Gid,
		target.HomeDir,
	)
	s.Require().NoError(os.WriteFile(filepath.Join(dir, "getent"), []byte(script), 0o600))
	s.Require().NoError(os.Chmod(filepath.Join(dir, "getent"), executableTestMode()))
	s.T().Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	s.Require().NoError(s.result(s.run(p)))
	s.Contains(a.output.String(), "reopened")
}

func (s *terminalTestSuite) TestValidationBeforeExecution() {
	a := newTestTerminal()
	dir := s.T().TempDir()
	result := &config.Result{
		MergedConfig:        &config.MergedDevContainerConfig{},
		ContainerDetails:    &config.ContainerDetails{},
		SubstitutionContext: &config.SubstitutionContext{ContainerWorkspaceFolder: dir},
	}
	result.MergedConfig.OnCreateCommands = []types.LifecycleHook{
		{"": {fmt.Sprintf("touch %s", filepath.Join(dir, "ran"))}},
	}
	result.MergedConfig.PostStartCommands = []types.LifecycleHook{
		{"a": {trueCommand}, "b": {trueCommand}},
	}
	_, err := RunPreAttachHooks(context.Background(), result, PreAttachOptions{Terminal: a})
	s.ErrorContains(err, "postStartCommand")
	_, err = os.Stat(filepath.Join(dir, "ran"))
	s.True(os.IsNotExist(err))
	_, err = SetupContainerPreAttach(
		context.Background(),
		&ContainerSetupConfig{SetupInfo: result, Terminal: a},
	)
	s.ErrorContains(err, "postStartCommand")
}

func (s *terminalTestSuite) TestFloorAndStoredWaitFor() {
	for _, configured := range []string{"", "postAttachCommand"} {
		s.Run(configured, func() {
			a := newTestTerminal()
			dir := s.T().TempDir()
			result := &config.Result{
				MergedConfig:        &config.MergedDevContainerConfig{},
				ContainerDetails:    &config.ContainerDetails{},
				SubstitutionContext: &config.SubstitutionContext{ContainerWorkspaceFolder: dir},
			}
			result.MergedConfig.WaitFor = configured
			result.MergedConfig.PostStartCommands = []types.LifecycleHook{{"": {trueCommand}}}
			result.MergedConfig.RemoteUser = s.username
			deferred, err := RunPreAttachHooks(
				context.Background(),
				result,
				PreAttachOptions{Terminal: a},
			)
			s.Require().NoError(err)
			s.Equal(configured, result.MergedConfig.WaitFor)
			s.Len(a.invocations, 1)
			for _, hook := range deferred.hooks {
				s.Nil(hook.params.terminal)
			}
		})
	}
}

func (s *terminalTestSuite) TestMarkerSkipAndShellFailure() {
	a := newTestTerminal()
	p := s.params(context.Background(), a, `printf ran`)
	p.name = fmt.Sprintf("interactive-test-%d", time.Now().UnixNano())
	p.content = "started"
	path := filepath.Join(containerDataDir(), p.name+".marker")
	s.T().Cleanup(func() { _ = os.Remove(path) })
	p.env.remoteUser = "nonexistent-interactive-user"
	s.Error(runHook(p))
	_, err := os.Stat(path)
	s.True(os.IsNotExist(err))
	p.env.remoteUser = s.username
	s.Require().NoError(runHook(p))
	s.Require().FileExists(path)
	p.env.remoteUser = "nonexistent-interactive-user"
	s.NoError(runHook(p))
	s.Len(a.invocations, 1)
}

func (s *terminalTestSuite) TestArgvAndLargeInput() {
	a := newTestTerminal()
	p := s.params(context.Background(), a, "")
	p.commands = []types.LifecycleHook{
		{
			"argv": {
				"sh",
				"-c",
				`stty raw -echo; printf 'ready:'; head -c 65536 /dev/zero | tr '\000' x; ` +
					`dd bs=1024 count=128 of=/dev/null 2>/dev/null; printf done`,
			},
		},
	}
	done := s.run(p)
	a.output.wait(s.T(), "ready:")
	written := make(chan struct{})
	go func() {
		defer close(written)
		_, _ = a.writer.Write(bytes.Repeat([]byte("z"), 128*1024))
	}()
	a.output.wait(s.T(), "done")
	s.NoError(s.result(done))
	<-written
}

func (s *terminalTestSuite) TestParentCancelAndExitAckSuccess() {
	a := newTestTerminal()
	ctx, cancel := context.WithCancel(context.Background())
	p := s.params(ctx, a, `printf 'ready:'; exec sleep 30`)
	run := s.run(p)
	a.output.wait(s.T(), "ready:")
	cancel()
	s.ErrorContains(s.result(run), "canceled")
	a = newTestTerminal()
	a.exitErr = fmt.Errorf("ack timeout")
	p = s.params(context.Background(), a, `printf 'final-output'`)
	s.NoError(s.result(s.run(p)))
	s.Contains(a.exitOutput, "final-output")
	s.Equal(0, a.exit.ExitCode)
}

func (s *terminalTestSuite) TestNoninteractivePostStartRemainsPipeBased() {
	result := &config.Result{
		MergedConfig:        &config.MergedDevContainerConfig{},
		ContainerDetails:    &config.ContainerDetails{},
		SubstitutionContext: &config.SubstitutionContext{ContainerWorkspaceFolder: s.T().TempDir()},
	}
	result.MergedConfig.RemoteUser = s.username
	result.MergedConfig.WaitFor = "postStartCommand"
	result.MergedConfig.PostStartCommands = []types.LifecycleHook{
		{"": {`test ! -t 0 && test ! -t 1`}},
	}
	_, err := RunPreAttachHooks(context.Background(), result, PreAttachOptions{})
	s.NoError(err)
}

func (s *terminalTestSuite) params(
	ctx context.Context,
	a *testTerminal,
	command string,
) hookRunParams {
	return hookRunParams{
		ctx:      ctx,
		commands: []types.LifecycleHook{{"": {command}}},
		env:      lifecycleEnv{remoteUser: s.username, workspaceFolder: s.T().TempDir()},
		name:     "postStartCommands",
		hook:     string(PhasePostStart),
		plan:     &hookPlan{},
		terminal: a,
	}
}

func (s *terminalTestSuite) run(p hookRunParams) <-chan error {
	done := make(chan error, 1)
	go func() { done <- runHook(p) }()
	return done
}

func (s *terminalTestSuite) result(done <-chan error) error {
	s.T().Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(12 * time.Second):
		s.T().Fatal("executor did not complete")
		return nil
	}
}

func executableTestMode() os.FileMode {
	return 0o600 | 0o100
}
