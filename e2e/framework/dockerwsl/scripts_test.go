//go:build !windows

package dockerwsl

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type shResult struct {
	code   int
	stdout string
	stderr string
}

func runShell(t *testing.T, args []string, env []string) shResult {
	t.Helper()
	//nolint:gosec // G204: tests run the production scripts under sh
	cmd := exec.Command("sh", args...)
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	res := shResult{stdout: stdout.String(), stderr: stderr.String()}
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		res.code = exitErr.ExitCode()
	} else {
		require.NoError(t, err)
	}
	return res
}

func shellSyntax(t *testing.T, body string) shResult {
	return runShell(t, []string{"-n", "-c", body}, nil)
}

func runScript(t *testing.T, body string, env []string, args ...string) shResult {
	return runShell(t, append([]string{"-c", body, "sh"}, args...), env)
}

// ScriptRunSuite executes the real shell scripts against a fake /proc.
type ScriptRunSuite struct {
	suite.Suite
	dir string
}

func TestScriptRunSuite(t *testing.T) { suite.Run(t, new(ScriptRunSuite)) }

func (s *ScriptRunSuite) SetupTest() { s.dir = s.T().TempDir() }

func (s *ScriptRunSuite) Env() []string { return []string{"DEVSY_PROC_ROOT=" + s.dir} }

// Spawn starts an orphaned sleeper and returns its pid.
func (s *ScriptRunSuite) Spawn() int {
	out, err := exec.Command("sh", "-c", "sleep 120 >/dev/null 2>&1 & echo $!").Output()
	s.Require().NoError(err)
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	return pid
}

func (s *ScriptRunSuite) Alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func (s *ScriptRunSuite) FakeProc(pid int, cmdline string) {
	dir := filepath.Join(s.dir, strconv.Itoa(pid))
	s.Require().NoError(os.MkdirAll(dir, 0o750))
	s.Require().NoError(os.WriteFile(filepath.Join(dir, "cmdline"), []byte(cmdline), 0o600))
}

func (s *ScriptRunSuite) Pidfile(content string) string {
	path := filepath.Join(s.dir, "x.pid")
	s.Require().NoError(os.WriteFile(path, []byte(content), 0o600))
	return path
}

func (s *ScriptRunSuite) Stop(pidfile string) shResult {
	return runScript(s.T(), stopOwnedScript, s.Env(), pidfile, "dockerd", dockerdMarker)
}

func (s *ScriptRunSuite) TestOwnedProcessIsStopped() {
	pid := s.Spawn()
	s.FakeProc(pid, "/usr/bin/dockerd\x00--host=unix:///x\x00"+dockerdMarker+"\x00")
	pidfile := s.Pidfile(strconv.Itoa(pid))

	res := s.Stop(pidfile)

	s.Equal(0, res.code, res.stderr)
	s.False(s.Alive(pid))
	s.NoFileExists(pidfile)
}

func (s *ScriptRunSuite) TestUnrelatedProcessIsNeverSignalled() {
	pid := s.Spawn()
	s.FakeProc(pid, "/usr/bin/vim\x00notes.txt\x00")
	pidfile := s.Pidfile(strconv.Itoa(pid))

	res := s.Stop(pidfile)

	s.Equal(scriptExitNotOwned, res.code)
	s.Contains(res.stderr, "not the dockerd owned by devsy-ci")
	s.True(s.Alive(pid), "an unrelated pid must survive")
	s.FileExists(pidfile)
}

func (s *ScriptRunSuite) TestSameProgramWithoutMarkerIsNotOwned() {
	pid := s.Spawn()
	s.FakeProc(pid, "/usr/bin/dockerd\x00--host=unix:///x\x00")
	res := s.Stop(s.Pidfile(strconv.Itoa(pid)))

	s.Equal(scriptExitNotOwned, res.code)
	s.True(s.Alive(pid), "another user's dockerd must survive")
}

func (s *ScriptRunSuite) TestMissingPidfileAndDeadPidAreIdempotent() {
	s.Equal(0, s.Stop(filepath.Join(s.dir, "absent.pid")).code)

	pid := s.Spawn()
	s.Require().NoError(syscall.Kill(pid, syscall.SIGKILL))
	s.Eventually(func() bool { return !s.Alive(pid) }, 5e9, 20e6)
	pidfile := s.Pidfile(strconv.Itoa(pid))
	s.Equal(0, s.Stop(pidfile).code)
	s.NoFileExists(pidfile, "a stale pidfile is cleared")
	s.Equal(0, s.Stop(pidfile).code, "second run is harmless")
}

func (s *ScriptRunSuite) TestGarbagePidfileIsReported() {
	for _, content := range []string{"", "abc", "12 34", "-1"} {
		res := s.Stop(s.Pidfile(content))
		s.Equal(3, res.code, content)
	}
}

func (s *ScriptRunSuite) TestListenerProbe() {
	net := filepath.Join(s.dir, "net")
	s.Require().NoError(os.MkdirAll(net, 0o750))
	writeTable := func(file, content string) {
		s.Require().NoError(os.WriteFile(filepath.Join(net, file), []byte(content), 0o600))
	}
	header := "  sl  local_address rem_address   st\n"
	listen := func(port, state string) string {
		return "   0: 00000000:" + port + " 00000000:0000 " + state + " 00000000:00000000\n"
	}
	run := func() int { return runScript(s.T(), tcpListenScript, s.Env(), sshPortHex).code }

	s.Equal(2, run(), "no /proc/net/tcp")

	writeTable("tcp", header+listen("0016", "0A")+listen(sshPortHex, "01"))
	s.Equal(1, run(), "port present but not in LISTEN state")

	writeTable("tcp", header+listen(sshPortHex, "0A"))
	s.Equal(0, run())

	writeTable("tcp", header)
	writeTable("tcp6", header+listen(sshPortHex, "0A"))
	s.Equal(0, run(), "IPv6 listener counts")

	res := runScript(s.T(), tcpListenScript, s.Env(), dockerTCPPortHex)
	s.Equal(1, res.code, "no listener on 2375")
}

func (s *ScriptRunSuite) TestScriptSyntaxIsValid() {
	for name, body := range map[string]string{
		"preflight": preflightScript, "install": installPackagesScript,
		"stop-installer": stopInstallerDaemonScript, "prepare": prepareRuntimeScript,
		"tcp": tcpListenScript, "create-user": createUserScript,
		"configure-sshd": configureSSHDScript, "install-key": installKeyScript,
		"stop-owned": stopOwnedScript, "remove-user": removeUserScript,
		"remove-files": removeFilesScript, "diagnostics": diagnosticsScript,
	} {
		res := shellSyntax(s.T(), body)
		s.Equal(0, res.code, "%s: %s", name, res.stderr)
	}
}
