package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

type DockerWSLFailureSuite struct {
	suite.Suite
	rig *wslRig
}

func TestDockerWSLFailureSuite(t *testing.T) { suite.Run(t, new(DockerWSLFailureSuite)) }

func (s *DockerWSLFailureSuite) SetupTest() {
	s.rig = newWSLRig(s.T())
	s.rig.files.files[testUserConfig] = []byte(originalSSHConfig)
}

type failureCase struct {
	name    string
	arrange func(*wslRig)
	code    Code
	// part must appear in the error text.
	part string
}

func exitWith(code int, stderr string) CommandResult {
	return CommandResult{ExitCode: code, Stderr: stderr}
}

func hostFailureCases() []failureCase {
	return []failureCase{
		{"distribution missing", func(r *wslRig) {
			r.onResult("wsl-list", CommandResult{Stdout: utf16le("Debian\r\n")})
		}, CodeDockerWSLDistroGone, "Ubuntu-24.04"},
		{"wsl list fails", func(r *wslRig) {
			r.onResult("wsl-list", exitWith(1, "boom"))
		}, CodeDockerWSLDistroGone, partExit1},
		{"tool missing", func(r *wslRig) {
			r.host.lookErr[toolSSHKeygen] = errors.New("not found")
		}, CodeConfigInvalid, "ssh-keygen.exe not found"},
		{"foreign daemon owns the socket", func(r *wslRig) {
			r.onResult(
				"wsl-preflight",
				CommandResult{Stdout: "socket=present\nci_user=absent\nleftovers=no\n"},
			)
		}, CodeDockerWSLDaemon, "refusing to stop it"},
		{"ci user already exists", func(r *wslRig) {
			r.onResult(
				"wsl-preflight",
				CommandResult{Stdout: "socket=absent\nci_user=present\nleftovers=no\n"},
			)
		}, CodeConfigInvalid, "already exists"},
		{"leftovers from a previous run", func(r *wslRig) {
			r.onResult(
				"wsl-preflight",
				CommandResult{Stdout: "socket=absent\nci_user=absent\nleftovers=yes\n"},
			)
		}, CodeConfigInvalid, "previous run"},
		{"apt fails", func(r *wslRig) {
			r.onResult(
				"wsl-install",
				exitWith(100, "E: Unable to locate package GH_ACCESS_TOKEN=hunter22secret"),
			)
		}, CodeDockerWSLInstall, "exit=100"},
		{"apt times out", func(r *wslRig) {
			r.onResult("wsl-install", CommandResult{TimedOut: true, ExitCode: -1})
		}, CodeDockerWSLInstall, "timed out"},
		{"installer daemon cannot be stopped", func(r *wslRig) {
			r.onResult("wsl-stop-installer", exitWith(1, "docker socket still present"))
		}, CodeDockerWSLDaemon, "docker socket still present"},
		{"dockerd launch fails", func(r *wslRig) {
			r.host.startErr = errors.New("CreateProcess: access denied")
		}, CodeDockerWSLDaemon, "access denied"},
		{"dockerd never ready", func(r *wslRig) {
			r.onResult(kindWSLDockerInfo, exitWith(1, "Cannot connect to the Docker daemon"))
		}, CodeDockerWSLDaemon, "did not become ready"},
		{"docker tcp listener", func(r *wslRig) {
			r.onResult("wsl-listener-"+dockerTCPPortHex, CommandResult{})
		}, CodeDockerWSLOracle, "unencrypted TCP"},
	}
}

func toolingFailureCases() []failureCase {
	return []failureCase{
		{"cli download 404", func(r *wslRig) {
			r.dl.err = errors.New("download: HTTP 404")
			r.dl.data = nil
		}, CodeDockerWSLInstall, "HTTP 404"},
		{"cli checksum mismatch", func(r *wslRig) {
			r.dl.data = zipOf(r.t, map[string]string{testExeEntry: "tampered"})
		}, CodeDockerCLIChecksum, "SHA-256 mismatch"},
		{"cli version unreadable", func(r *wslRig) {
			r.onResult("docker-version", CommandResult{Stdout: "garbage"})
		}, CodeDockerWSLInstall, "did not report"},
		{"ssh-keygen fails", func(r *wslRig) {
			r.onResult("keygen", exitWith(1, "no space"))
		}, CodeDockerWSLSSHNotReady, partExit1},
		{"key ACL step fails", func(r *wslRig) {
			r.onResult("icacls-restrict", exitWith(5, "Access is denied."))
		}, CodeDockerWSLSSHNotReady, "exit=5"},
		{"key ACL too broad", func(r *wslRig) {
			r.on("icacls-show", func(_ context.Context, c Command) (CommandResult, error) {
				return CommandResult{
					Stdout: c.Args[0] + ` HOST\runner:(R)` + "\n" + ` BUILTIN\Users:(R)` + "\n",
				}, nil
			})
		}, CodeDockerWSLSSHNotReady, "grants access"},
		{"authorized key install fails", func(r *wslRig) {
			r.onResult("wsl-install-key", exitWith(1, "chown: invalid user"))
		}, CodeDockerWSLSSHNotReady, partExit1},
		{"sshd never listens", func(r *wslRig) {
			r.onResult("wsl-listener-"+sshPortHex, CommandResult{ExitCode: 1})
		}, CodeDockerWSLSSHNotReady, "did not start listening"},
		{"host key unreadable", func(r *wslRig) {
			r.onResult("wsl-cat-hostkey", CommandResult{Stdout: "not a key"})
		}, CodeDockerWSLSSHNotReady, "host public key"},
		{"no address presents a banner", func(r *wslRig) {
			r.dialer.answer = map[string]string{}
		}, CodeDockerWSLSSHNotReady, "127.0.0.1:2222"},
	}
}

func connectivityFailureCases() []failureCase {
	return []failureCase{
		{"authentication fails", func(r *wslRig) {
			r.onResult("ssh", exitWith(255, "devsyci@host: Permission denied (publickey)."))
		}, CodeDockerWSLSSHAuth, "Permission denied"},
		{"host key mismatch", func(r *wslRig) {
			r.onResult("ssh", exitWith(255, "Host key verification failed."))
		}, CodeDockerWSLEndpoint, "Host key verification failed"},
		{"ssh unreachable", func(r *wslRig) {
			r.onResult("ssh", exitWith(255, "ssh: connect to host: Connection refused"))
		}, CodeDockerWSLSSHNotReady, "Connection refused"},
		{"docker over ssh fails", func(r *wslRig) {
			r.onResult(kindDockerInfoSSH, exitWith(1, "error during connect"))
		}, CodeDockerWSLEndpoint, partExit1},
		{"wrong daemon id", func(r *wslRig) {
			r.runner.idSSH = "ZZZZ:YYYY:XXXX:WWWW"
		}, CodeDockerWSLEndpoint, "differs"},
		{"dead pipe unexpectedly succeeds", func(r *wslRig) {
			r.onResult(kindDockerInfoDead, CommandResult{Stdout: "Server: Docker"})
		}, CodeDockerWSLOracle, "regression oracle would be void"},
		{"dead pipe oracle times out", func(r *wslRig) {
			r.onResult(kindDockerInfoDead, CommandResult{TimedOut: true, ExitCode: -1})
		}, CodeDockerWSLOracle, "timed out"},
	}
}

func failureCases() []failureCase {
	return slices.Concat(hostFailureCases(), toolingFailureCases(), connectivityFailureCases())
}

func (s *DockerWSLFailureSuite) TestEveryFailureIsTypedFailsClosedAndCleansUp() {
	for _, tc := range failureCases() {
		s.Run(tc.name, func() {
			s.SetupTest()
			tc.arrange(s.rig)

			err := s.rig.setup()

			s.Require().Error(err)
			s.Equal([]Code{tc.code}, codesOf(err))
			s.Contains(err.Error(), tc.part)
			s.NotContains(err.Error(), "hunter22secret")
			s.NotContains(
				s.rig.files.text(testGHEnv),
				"DEVSY_E2E_",
				"no export before every proof passes",
			)
			s.AssertCleanupRestoresEverything()
		})
	}
}

func (s *DockerWSLFailureSuite) AssertCleanupRestoresEverything() {
	// The fault that failed setup is fixed before cleanup runs.
	s.rig.runner.overrides = map[string]func(context.Context, Command) (CommandResult, error){}
	s.rig.host.startErr = nil
	s.Require().NoError(s.rig.cleanup())
	s.False(s.rig.files.has(testStateFile), "cleanup removes the state it fully handled")
	s.Empty(s.rig.files.under(testFixture), "fixture tree is gone")
	s.Equal(originalSSHConfig, s.rig.files.text(testUserConfig), "user ssh config is byte-exact")
}

func (s *DockerWSLFailureSuite) TestFailureCarriesBoundedSanitizedDiagnostics() {
	secret := "GH_ACCESS_TOKEN=supersecretvalue"
	s.rig.onResult("wsl-diagnostics", CommandResult{
		Stdout: "== tail dockerd.log\nfatal: " + secret + "\n" + strings.Repeat(
			"x",
			3*maxDetailBytes,
		),
	})
	s.rig.onResult(kindWSLDockerInfo, exitWith(1, "no daemon"))

	err := s.rig.setup()

	s.Require().Error(err)
	se, ok := errors.AsType[*StageError](err)
	s.Require().True(ok)
	s.LessOrEqual(len(se.Detail), maxDetailBytes+len("[truncated] "), "detail is bounded")
	s.NotContains(err.Error(), "supersecretvalue")
	s.Equal(testFixtureDiagnostics(), se.Artifact)
	diag := s.rig.files.text(se.Artifact)
	s.Contains(diag, "setup-failure:"+stageDaemon)
	s.NotContains(diag, "supersecretvalue")
	s.LessOrEqual(len(diag), maxDiagnosticBytes+256)
}

func testFixtureDiagnostics() string { return "/tmp/rt/devsy-ci/" + diagnosticsFile }

func (s *DockerWSLFailureSuite) TestDaemonReadinessPollIsBounded() {
	s.rig.onResult(kindWSLDockerInfo, exitWith(1, "no daemon"))
	s.Require().Error(s.rig.setup())
	s.Equal(daemonReadyAttempts, s.rig.runner.count(kindWSLDockerInfo))
	s.Equal(0, s.rig.runner.count("docker-version"), "later stages never ran")
}

func (s *DockerWSLFailureSuite) TestAuthenticationFailureIsNotRetried() {
	s.rig.onResult("ssh", exitWith(255, "Permission denied (publickey)."))
	s.Require().Error(s.rig.setup())
	s.Equal(1, s.rig.runner.count("ssh"))
	s.Equal(1, s.rig.runner.count("ssh-verbose"), "one verbose transcript for diagnosis")
}

func (s *DockerWSLFailureSuite) TestUnverifiedCLINeverRuns() {
	s.rig.dl.data = zipOf(s.T(), map[string]string{testExeEntry: "tampered"})
	s.Require().Error(s.rig.setup())
	s.Equal(0, s.rig.runner.count("docker-version"))
	s.Empty(s.rig.files.under(testFixture + "/docker-cli"))
}

func (s *DockerWSLFailureSuite) TestFailedBannerReportsBothAddressesAndSkipsConfig() {
	s.rig.dialer.answer = map[string]string{}
	s.Require().Error(s.rig.setup())
	s.Equal(
		originalSSHConfig,
		s.rig.files.text(testUserConfig),
		"ssh config untouched before an address is chosen",
	)
}

func (s *DockerWSLFailureSuite) TestPartiallyInstalledSSHConfigIsRestored() {
	s.rig.files.failWrite[testUserConfig] = errors.New("disk full")

	err := s.rig.setup()

	s.Equal([]Code{CodeDockerWSLSSHNotReady}, codesOf(err))
	s.Equal(originalSSHConfig, s.rig.files.text(testUserConfig))
	st := s.rig.state()
	s.Equal(testUserConfig, st.Owned.SSHConfig, "registered before the write was attempted")
	s.Empty(st.Owned.SSHConfigWritten)
	s.rig.files.failWrite = map[string]error{}
	s.AssertCleanupRestoresEverything()
}

func (s *DockerWSLFailureSuite) TestExistingBlockForThisRunIsRejected() {
	block := sshConfigBlock(testSpec())
	s.rig.files.files[testUserConfig] = []byte(block + "\n" + originalSSHConfig)

	err := s.rig.setup()

	s.Equal([]Code{CodeConfigInvalid}, codesOf(err))
	s.Contains(err.Error(), "already holds a devsy-ci block")
}

func (s *DockerWSLFailureSuite) TestStatePersistedBeforeEachLaterFailure() {
	s.rig.onResult("wsl-install-key", exitWith(1, "boom"))
	s.Require().Error(s.rig.setup())

	st := s.rig.state()
	s.Equal(stageIdentity, st.Stage, "last completed stage")
	s.Equal(ciUser, st.Owned.CIUser)
	s.Equal(sshdConfigDir, st.Owned.SSHDConfigDir)
	s.Equal(dockerPidfile, st.Owned.DockerPidfile)
	s.Equal(testDaemonID, st.DaemonID)
	s.Equal(testFixture+"/id_ed25519", st.Owned.SSHIdentityFile)
	s.Empty(st.Owned.SSHDPidfile, "sshd was never started")
	s.Empty(st.Owned.SSHConfig, "ssh config was never touched")
}
