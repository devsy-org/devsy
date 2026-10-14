package dockerwsl

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/e2e/framework/runenv"
	"github.com/devsy-org/devsy/e2e/framework/runenv/runenvtest"
	"github.com/stretchr/testify/suite"
)

type DockerWSLFailureSuite struct {
	suite.Suite
	rig *wslRig
}

func TestDockerWSLFailureSuite(t *testing.T) { suite.Run(t, new(DockerWSLFailureSuite)) }

func (s *DockerWSLFailureSuite) SetupTest() {
	s.rig = newWSLRig(s.T())
	s.rig.files.Data[testUserConfig] = []byte(originalSSHConfig)
}

type failureCase struct {
	name    string
	arrange func(*wslRig)
	code    runenv.Code
	// part must appear in the error text.
	part string
}

func exitWith(code int, stderr string) runenv.CommandResult {
	return runenv.CommandResult{ExitCode: code, Stderr: stderr}
}

func hostFailureCases() []failureCase {
	return []failureCase{
		{"distribution missing", func(r *wslRig) {
			r.onResult("wsl-list", runenv.CommandResult{Stdout: utf16le("Debian\r\n")})
		}, runenv.CodeDockerWSLDistroGone, "Ubuntu-24.04"},
		{"wsl list fails", func(r *wslRig) {
			r.onResult("wsl-list", exitWith(1, "boom"))
		}, runenv.CodeDockerWSLDistroGone, partExit1},
		{"tool missing", func(r *wslRig) {
			r.host.lookErr[toolSSHKeygen] = errors.New("not found")
		}, runenv.CodeConfigInvalid, "ssh-keygen.exe not found"},
		{"foreign daemon owns the socket", func(r *wslRig) {
			r.onResult(
				"wsl-preflight",
				runenv.CommandResult{Stdout: "socket=present\nci_user=absent\nleftovers=no\n"},
			)
		}, runenv.CodeDockerWSLDaemon, "refusing to stop it"},
		{"ci user already exists", func(r *wslRig) {
			r.onResult(
				"wsl-preflight",
				runenv.CommandResult{Stdout: "socket=absent\nci_user=present\nleftovers=no\n"},
			)
		}, runenv.CodeConfigInvalid, "already exists"},
		{"leftovers from a previous run", func(r *wslRig) {
			r.onResult(
				"wsl-preflight",
				runenv.CommandResult{Stdout: "socket=absent\nci_user=absent\nleftovers=yes\n"},
			)
		}, runenv.CodeConfigInvalid, "previous run"},
		{"apt fails", func(r *wslRig) {
			r.onResult(
				"wsl-install",
				exitWith(100, "E: Unable to locate package GH_ACCESS_TOKEN=hunter22secret"),
			)
		}, runenv.CodeDockerWSLInstall, "exit=100"},
		{"apt times out", func(r *wslRig) {
			r.onResult("wsl-install", runenv.CommandResult{TimedOut: true, ExitCode: -1})
		}, runenv.CodeDockerWSLInstall, "timed out"},
		{"installer daemon cannot be stopped", func(r *wslRig) {
			r.onResult("wsl-stop-installer", exitWith(1, "installer dockerd still running"))
		}, runenv.CodeDockerWSLDaemon, "installer dockerd still running"},
		{"dockerd launch fails", func(r *wslRig) {
			r.host.startErr = errors.New("CreateProcess: access denied")
		}, runenv.CodeDockerWSLDaemon, "access denied"},
		{"dockerd never ready", func(r *wslRig) {
			r.onResult(kindWSLDockerInfo, exitWith(1, "Cannot connect to the Docker daemon"))
		}, runenv.CodeDockerWSLDaemon, "did not become ready"},
		{"docker tcp listener", func(r *wslRig) {
			r.onResult("wsl-listener-"+dockerTCPPortHex, runenv.CommandResult{})
		}, runenv.CodeDockerWSLOracle, "unencrypted TCP"},
	}
}

func toolingFailureCases() []failureCase {
	return []failureCase{
		{"cli download 404", func(r *wslRig) {
			r.dl.err = errors.New("download: HTTP 404")
			r.dl.data = nil
		}, runenv.CodeDockerWSLInstall, "HTTP 404"},
		{"cli checksum mismatch", func(r *wslRig) {
			r.dl.data = zipOf(r.t, map[string]string{testExeEntry: "tampered"})
		}, runenv.CodeDockerCLIChecksum, "SHA-256 mismatch"},
		{"cli version unreadable", func(r *wslRig) {
			r.onResult("docker-version", runenv.CommandResult{Stdout: "garbage"})
		}, runenv.CodeDockerWSLInstall, "did not report"},
		{"ssh-keygen fails", func(r *wslRig) {
			r.onResult("keygen", exitWith(1, "no space"))
		}, runenv.CodeDockerWSLSSHNotReady, partExit1},
		{"key ACL step fails", func(r *wslRig) {
			r.onResult("icacls-restrict", exitWith(5, "Access is denied."))
		}, runenv.CodeDockerWSLSSHNotReady, "exit=5"},
		{"key ACL too broad", func(r *wslRig) {
			r.on(
				"icacls-show",
				func(_ context.Context, c runenv.Command) (runenv.CommandResult, error) {
					return runenv.CommandResult{
						Stdout: c.Args[0] + ` HOST\runner:(R)` + "\n" + ` BUILTIN\Users:(R)` + "\n",
					}, nil
				},
			)
		}, runenv.CodeDockerWSLSSHNotReady, "grants access"},
		{"authorized key install fails", func(r *wslRig) {
			r.onResult("wsl-install-key", exitWith(1, "chown: invalid user"))
		}, runenv.CodeDockerWSLSSHNotReady, partExit1},
		{"sshd never listens", func(r *wslRig) {
			r.onResult("wsl-listener-"+sshPortHex, runenv.CommandResult{ExitCode: 1})
		}, runenv.CodeDockerWSLSSHNotReady, "did not start listening"},
		{"host key unreadable", func(r *wslRig) {
			r.onResult("wsl-cat-hostkey", runenv.CommandResult{Stdout: "not a key"})
		}, runenv.CodeDockerWSLSSHNotReady, "host public key"},
		{"no address presents a banner", func(r *wslRig) {
			r.dialer.answer = map[string]string{}
		}, runenv.CodeDockerWSLSSHNotReady, "127.0.0.1:2222"},
	}
}

func connectivityFailureCases() []failureCase {
	return []failureCase{
		{"authentication fails", func(r *wslRig) {
			r.onResult("ssh", exitWith(255, "devsyci@host: Permission denied (publickey)."))
		}, runenv.CodeDockerWSLSSHAuth, "Permission denied"},
		{"host key mismatch", func(r *wslRig) {
			r.onResult("ssh", exitWith(255, "Host key verification failed."))
		}, runenv.CodeDockerWSLEndpoint, "Host key verification failed"},
		{"ssh unreachable", func(r *wslRig) {
			r.onResult("ssh", exitWith(255, "ssh: connect to host: Connection refused"))
		}, runenv.CodeDockerWSLSSHNotReady, "Connection refused"},
		{"docker over ssh fails", func(r *wslRig) {
			r.onResult(kindDockerInfoSSH, exitWith(1, "error during connect"))
		}, runenv.CodeDockerWSLEndpoint, partExit1},
		{"wrong daemon id", func(r *wslRig) {
			r.runner.idSSH = "ZZZZ:YYYY:XXXX:WWWW"
		}, runenv.CodeDockerWSLEndpoint, "differs"},
		{"dead pipe unexpectedly succeeds", func(r *wslRig) {
			r.onResult(kindDockerInfoDead, runenv.CommandResult{Stdout: "Server: Docker"})
		}, runenv.CodeDockerWSLOracle, "regression oracle would be void"},
		{"dead pipe oracle times out", func(r *wslRig) {
			r.onResult(kindDockerInfoDead, runenv.CommandResult{TimedOut: true, ExitCode: -1})
		}, runenv.CodeDockerWSLOracle, "timed out"},
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
			s.Equal([]runenv.Code{tc.code}, runenvtest.CodesOf(err))
			s.Contains(err.Error(), tc.part)
			s.NotContains(err.Error(), "hunter22secret")
			s.NotContains(
				s.rig.files.Text(testGHEnv),
				"DEVSY_E2E_",
				"no export before every proof passes",
			)
			s.AssertCleanupRestoresEverything()
		})
	}
}

func (s *DockerWSLFailureSuite) AssertCleanupRestoresEverything() {
	// The fault that failed setup is fixed before cleanup runs.
	s.rig.runner.overrides = map[string]func(context.Context, runenv.Command) (runenv.CommandResult, error){}
	s.rig.host.startErr = nil
	s.Require().NoError(s.rig.cleanup())
	s.False(s.rig.files.Has(testStateFile), "cleanup removes the state it fully handled")
	s.Empty(s.rig.files.Under(testFixture), "fixture tree is gone")
	s.Equal(originalSSHConfig, s.rig.files.Text(testUserConfig), "user ssh config is byte-exact")
}

func (s *DockerWSLFailureSuite) TestFailureCarriesBoundedSanitizedDiagnostics() {
	secret := "GH_ACCESS_TOKEN=supersecretvalue"
	s.rig.onResult("wsl-diagnostics", runenv.CommandResult{
		Stdout: "== tail dockerd.log\nfatal: " + secret + "\n" + strings.Repeat(
			"x",
			3*runenv.MaxDetailBytes,
		),
	})
	s.rig.onResult(kindWSLDockerInfo, exitWith(1, "no daemon"))

	err := s.rig.setup()

	s.Require().Error(err)
	se, ok := errors.AsType[*runenv.StageError](err)
	s.Require().True(ok)
	s.LessOrEqual(len(se.Detail), runenv.MaxDetailBytes+len("[truncated] "), "detail is bounded")
	s.NotContains(err.Error(), "supersecretvalue")
	s.Equal(testFixtureDiagnostics(), se.Artifact)
	diag := s.rig.files.Text(se.Artifact)
	s.Contains(diag, "setup-failure:"+stageDaemon)
	s.NotContains(diag, "supersecretvalue")
	s.LessOrEqual(len(diag), maxDiagnosticBytes+256)
}

func testFixtureDiagnostics() string { return filepath.Join(testStateDir, diagnosticsFile) }

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

func (s *DockerWSLFailureSuite) TestSSHDockerInfoAuthenticationFailureIsNotRetried() {
	s.rig.onResult(kindDockerInfoSSH, exitWith(255, "Permission denied (publickey)."))

	err := s.rig.setup()

	s.Equal([]runenv.Code{runenv.CodeDockerWSLEndpoint}, runenvtest.CodesOf(err))
	s.Equal(1, s.rig.runner.count(kindDockerInfoSSH))
}

func (s *DockerWSLFailureSuite) TestSSHDockerInfoRetriesAreBounded() {
	s.rig.onResult(kindDockerInfoSSH, runenv.CommandResult{ExitCode: -1, TimedOut: true})

	err := s.rig.setup()

	s.Equal([]runenv.Code{runenv.CodeDockerWSLEndpoint}, runenvtest.CodesOf(err))
	s.Contains(err.Error(), "timed out")
	s.Equal(sshAuthAttempts, s.rig.runner.count(kindDockerInfoSSH))
}

func (s *DockerWSLFailureSuite) TestUnverifiedCLINeverRuns() {
	s.rig.dl.data = zipOf(s.T(), map[string]string{testExeEntry: "tampered"})
	s.Require().Error(s.rig.setup())
	s.Equal(0, s.rig.runner.count("docker-version"))
	s.Empty(s.rig.files.Under(fixtureFile("docker-cli")))
}

func (s *DockerWSLFailureSuite) TestFailedBannerReportsBothAddressesAndSkipsConfig() {
	s.rig.dialer.answer = map[string]string{}
	s.Require().Error(s.rig.setup())
	s.Equal(
		originalSSHConfig,
		s.rig.files.Text(testUserConfig),
		"ssh config untouched before an address is chosen",
	)
}

func (s *DockerWSLFailureSuite) TestPartiallyInstalledSSHConfigIsRestored() {
	s.rig.files.FailWrite[testUserConfig] = errors.New("disk full")

	err := s.rig.setup()

	s.Equal([]runenv.Code{runenv.CodeDockerWSLSSHNotReady}, runenvtest.CodesOf(err))
	s.Equal(originalSSHConfig, s.rig.files.Text(testUserConfig))
	st := s.rig.state()
	s.Equal(testUserConfig, st.Owned.SSHConfig, "registered before the write was attempted")
	s.Empty(st.Owned.SSHConfigWritten)
	s.rig.files.FailWrite = map[string]error{}
	s.AssertCleanupRestoresEverything()
}

func (s *DockerWSLFailureSuite) TestExistingBlockForThisRunIsRejected() {
	block := sshConfigBlock(testSpec())
	s.rig.files.Data[testUserConfig] = []byte(block + "\n" + originalSSHConfig)

	err := s.rig.setup()

	s.Equal([]runenv.Code{runenv.CodeConfigInvalid}, runenvtest.CodesOf(err))
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
	s.Equal(fixtureFile("id_ed25519"), st.Owned.SSHIdentityFile)
	s.Empty(st.Owned.SSHDPidfile, "sshd was never started")
	s.Empty(st.Owned.SSHConfig, "ssh config was never touched")
}
