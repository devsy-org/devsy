package main

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

type DockerWSLCleanupSuite struct {
	suite.Suite
	rig *wslRig
}

func TestDockerWSLCleanupSuite(t *testing.T) { suite.Run(t, new(DockerWSLCleanupSuite)) }

func (s *DockerWSLCleanupSuite) SetupTest() {
	s.rig = newWSLRig(s.T())
	s.rig.files.files[testUserConfig] = []byte(originalSSHConfig)
}

func (s *DockerWSLCleanupSuite) SetupOK() {
	s.Require().NoError(s.rig.setup())
	s.rig.runner.reset()
}

func (s *DockerWSLCleanupSuite) TestFullTeardownInReverseOrder() {
	s.SetupOK()

	s.Require().NoError(s.rig.cleanup())

	r := s.rig.runner
	order := []string{
		"icacls-restore", "wsl-stop-owned:" + sshdPidfile, "wsl-remove-user",
		"wsl-stop-owned:" + dockerPidfile, "wsl-remove-files", "wsl-listener-" + dockerTCPPortHex,
	}
	last := -1
	for _, kind := range order {
		idx := r.index(kind)
		s.Greater(idx, last, "%s must run after the previous step (kinds: %v)", kind, r.kinds)
		last = idx
	}
	s.False(s.rig.files.has(testStateFile))
	s.Empty(s.rig.files.under(testFixture))
	s.Equal(originalSSHConfig, s.rig.files.text(testUserConfig))
	s.NotContains(s.rig.allArgs(), "pkill")
}

func (s *DockerWSLCleanupSuite) TestStopScriptsAreScopedToOwnedPidfilesAndMarkers() {
	s.SetupOK()
	s.Require().NoError(s.rig.cleanup())

	var stops []string
	for i, kind := range s.rig.runner.kinds {
		if strings.HasPrefix(kind, "wsl-stop-owned:") {
			stops = append(stops, strings.Join(s.rig.runner.calls[i].Args, " "))
		}
	}
	s.Require().Len(stops, 2)
	s.Contains(stops[0], sshdPidfile+" sshd "+sshdMarker)
	s.Contains(stops[1], dockerPidfile+" dockerd "+dockerdMarker)
}

func (s *DockerWSLCleanupSuite) TestSSHConfigThatDidNotExistIsRemoved() {
	delete(s.rig.files.files, testUserConfig)
	s.Require().NoError(s.rig.setup())
	s.True(s.rig.files.has(testUserConfig))

	s.Require().NoError(s.rig.cleanup())

	s.False(s.rig.files.has(testUserConfig))
	s.Equal(0, s.rig.runner.count("icacls-save"), "no ACLs to save for a new file")
	s.Equal(0, s.rig.runner.count("icacls-restore"))
}

func (s *DockerWSLCleanupSuite) TestLaterUserEditsSurviveBlockRemoval() {
	s.SetupOK()
	edited := s.rig.files.text(testUserConfig) + "Host later\r\n  Port 1\r\n"
	s.rig.files.files[testUserConfig] = []byte(edited)

	s.Require().NoError(s.rig.cleanup())

	s.Equal(originalSSHConfig+"Host later\r\n  Port 1\r\n", s.rig.files.text(testUserConfig))
}

func (s *DockerWSLCleanupSuite) TestUserEditedAwayBlockIsLeftAlone() {
	s.SetupOK()
	s.rig.files.files[testUserConfig] = []byte("# replaced by user\n")

	s.Require().NoError(s.rig.cleanup())

	s.Equal("# replaced by user\n", s.rig.files.text(testUserConfig))
}

func (s *DockerWSLCleanupSuite) TestMissingStateIsASafeNoOp() {
	s.Require().NoError(s.rig.cleanup())
	s.Empty(s.rig.runner.calls)
	s.Contains(s.rig.out.String(), "nothing to clean up")
}

func (s *DockerWSLCleanupSuite) TestCleanupTwiceIsHarmless() {
	s.SetupOK()
	s.Require().NoError(s.rig.cleanup())
	s.rig.runner.reset()

	s.Require().NoError(s.rig.cleanup())

	s.Empty(s.rig.runner.calls)
}

func (s *DockerWSLCleanupSuite) TestWrongJobStateIsRejectedUntouched() {
	other := newDockerWSLState("99-1-e2e", defaultDistribution)
	other.Owned.CIUser = ciUser
	s.Require().NoError(newStateStore(s.rig.files, testStateFile).save(other))
	before := s.rig.files.text(testStateFile)

	err := s.rig.cleanup()

	s.Equal([]Code{CodeConfigInvalid}, codesOf(err))
	s.Empty(s.rig.runner.calls)
	s.Equal(before, s.rig.files.text(testStateFile))
}

func (s *DockerWSLCleanupSuite) TestTamperedPathsAreNeverDeleted() {
	for name, mutate := range map[string]func(*dockerWSLState){
		"identity outside fixture": func(st *dockerWSLState) { st.Owned.SSHIdentityFile = etcPasswd },
		"cli dir is state dir":     func(st *dockerWSLState) { st.Owned.DockerCLIDirectory = testStateDir },
		"traversal":                func(st *dockerWSLState) { st.Owned.DockerConfigDir = testFixture + "/../../x" },
		"other user":               func(st *dockerWSLState) { st.Owned.CIUser = "root" },
		"version":                  func(st *dockerWSLState) { st.Version = 9 },
	} {
		s.Run(name, func() {
			s.SetupTest()
			s.rig.files.files[etcPasswd] = []byte("root")
			s.rig.files.files["/tmp/rt/devsy-ci/keepme"] = []byte("x")
			st := newDockerWSLState(testRunID, defaultDistribution)
			mutate(st)
			s.Require().NoError(newStateStore(s.rig.files, testStateFile).save(st))

			err := s.rig.cleanup()

			s.Require().Error(err)
			s.True(s.rig.files.has(etcPasswd))
			s.True(s.rig.files.has("/tmp/rt/devsy-ci/keepme"))
			s.Empty(s.rig.runner.calls)
		})
	}
}

func (s *DockerWSLCleanupSuite) TestOwnedProcessMismatchIsAReportedFailure() {
	s.SetupOK()
	s.rig.onResult(
		"wsl-stop-owned:"+dockerPidfile,
		CommandResult{
			ExitCode: scriptExitNotOwned,
			Stderr:   "pid 42 is not the dockerd owned by devsy-ci",
		},
	)

	err := s.rig.cleanup()

	s.Require().Error(err)
	s.Equal([]Code{CodeDockerWSLCleanup}, codesOf(err))
	s.Equal(exitCodeFailure, exitCodeForError(err), "a failed cleanup must fail the CI step")
	s.Contains(err.Error(), "stop-dockerd")
	s.Contains(err.Error(), "not the dockerd")
	r := s.rig.runner
	s.Equal(0, r.count("wsl-remove-files"), "files stay while an owned process may live")
	s.Equal(0, r.count("wsl-listener-"+dockerTCPPortHex))
	s.Equal(1, r.count("wsl-remove-user"), "independent steps still run")
	s.True(s.rig.files.has(testStateFile), "state is kept so cleanup can be retried")
	var attempts int
	for _, rec := range s.rig.state().Teardown {
		if rec.Step == stepStopDockerd {
			attempts = rec.Attempts
			s.NotEmpty(rec.Error)
		}
	}
	s.Equal(1, attempts)
	s.Equal(originalSSHConfig, s.rig.files.text(testUserConfig))
}

func (s *DockerWSLCleanupSuite) TestRetryAfterFailureFinishesTheJob() {
	s.SetupOK()
	s.rig.onResult("wsl-stop-owned:"+dockerPidfile, exitWith(5, "pid survived SIGKILL"))
	s.Require().Error(s.rig.cleanup())
	delete(s.rig.runner.overrides, "wsl-stop-owned:"+dockerPidfile)

	s.Require().NoError(s.rig.cleanup())

	s.False(s.rig.files.has(testStateFile))
	s.Equal(1, s.rig.runner.count("wsl-remove-files"))
}

func (s *DockerWSLCleanupSuite) TestFailedRestoreKeepsTheBackup() {
	s.SetupOK()
	s.rig.files.failWrite[testUserConfig] = errTest("access denied")

	err := s.rig.cleanup()

	s.Require().Error(err)
	s.Contains(err.Error(), stepRestoreSSHConfig)
	s.Equal(originalSSHConfig, s.rig.files.text(testFixture+"/ssh-config-original"),
		"the backup survives a failed restore")
	s.True(s.rig.files.has(testStateFile))

	s.rig.files.failWrite = map[string]error{}
	s.Require().NoError(s.rig.cleanup())
	s.Equal(originalSSHConfig, s.rig.files.text(testUserConfig))
}

func (s *DockerWSLCleanupSuite) TestLeftoverDockerTCPListenerFailsCleanup() {
	s.SetupOK()
	s.rig.onResult("wsl-listener-"+dockerTCPPortHex, CommandResult{})

	err := s.rig.cleanup()

	s.Equal([]Code{CodeDockerWSLCleanup}, codesOf(err))
	s.Contains(err.Error(), "2375")
}

func (s *DockerWSLCleanupSuite) TestACLRestoreFailureIsReported() {
	s.SetupOK()
	s.rig.onResult("icacls-restore", exitWith(5, "Access is denied."))

	err := s.rig.cleanup()

	s.Require().Error(err)
	s.Contains(err.Error(), stepRestoreSSHConfig)
}

func (s *DockerWSLCleanupSuite) TestStateWithNoLinuxResourcesNeedsNoWSL() {
	s.rig.onResult("wsl-list", exitWith(1, "x"))
	s.rig.host.lookErr[toolWSL] = errTest("no wsl")
	s.Require().NoError(newStateStore(s.rig.files, testStateFile).save(
		newDockerWSLState(testRunID, defaultDistribution)))

	s.Require().NoError(s.rig.cleanup())

	s.Empty(s.rig.runner.calls)
	s.False(s.rig.files.has(testStateFile))
}

// Section 9.3: state created -> packages installed -> dockerd launch fails ->
// cleanup executes.
func (s *DockerWSLCleanupSuite) TestPartialProgressDockerdLaunchFails() {
	s.rig.host.startErr = errTest("launch failed")
	s.Require().Error(s.rig.setup())
	st := s.rig.state()
	s.Equal(stagePackages, st.Stage)
	s.Equal(dockerPidfile, st.Owned.DockerPidfile)
	s.Empty(st.Owned.CIUser)

	s.rig.runner.reset()
	s.Require().NoError(s.rig.cleanup())

	r := s.rig.runner
	s.Equal(1, r.count("wsl-stop-owned:"+dockerPidfile))
	s.Equal(0, r.count("wsl-stop-owned:"+sshdPidfile), "sshd was never owned")
	s.Equal(0, r.count("wsl-remove-user"))
	s.Equal(1, r.count("wsl-remove-files"))
	s.False(s.rig.files.has(testStateFile))

	r.reset()
	s.Require().NoError(s.rig.cleanup(), "second invocation")
	s.Empty(r.calls)
}

// Section 9.3: SSH key exists -> authorized-key install fails -> cleanup runs.
func (s *DockerWSLCleanupSuite) TestPartialProgressAuthorizedKeyInstallFails() {
	s.rig.onResult("wsl-install-key", exitWith(1, "install failed"))
	s.Require().Error(s.rig.setup())
	s.True(s.rig.files.has(testFixture + "/id_ed25519"))

	s.rig.runner.reset()
	s.Require().NoError(s.rig.cleanup())

	r := s.rig.runner
	s.False(s.rig.files.has(testFixture + "/id_ed25519"))
	s.False(s.rig.files.has(testFixture + "/id_ed25519.pub"))
	s.Equal(1, r.count("wsl-remove-user"))
	s.Equal(0, r.count("wsl-stop-owned:"+sshdPidfile))
	s.Equal(1, r.count("wsl-stop-owned:"+dockerPidfile))

	r.reset()
	s.Require().NoError(s.rig.cleanup(), "second invocation")
	s.Empty(r.calls)
}

func (s *DockerWSLCleanupSuite) TestCleanupCollectsDiagnosticsBeforeRemovingLogs() {
	s.SetupOK()
	s.rig.onResult("wsl-diagnostics", CommandResult{Stdout: "== tail dockerd.log\nlast words\n"})

	s.Require().NoError(s.rig.cleanup())

	r := s.rig.runner
	s.Less(r.index("wsl-diagnostics"), r.index("wsl-remove-files"))
	s.Contains(s.rig.files.text(testFixtureDiagnostics()), "last words")
}

// Preflight tests.

func (s *DockerWSLCleanupSuite) ExportEnv() {
	s.rig.env = append(
		s.rig.env,
		envSSHDockerHost+"="+wantEndpoint,
		envWSLDaemonID+"="+testDaemonID,
	)
}

func (s *DockerWSLCleanupSuite) Preflight() error {
	return s.rig.controller().preflight(context.Background())
}

func (s *DockerWSLCleanupSuite) TestPreflightAcceptsCompletedSetup() {
	s.Require().NoError(s.rig.setup())
	s.ExportEnv()
	s.Require().NoError(s.Preflight())
}

func (s *DockerWSLCleanupSuite) TestPreflightRejections() {
	s.Require().NoError(s.rig.setup())
	cases := map[string]func(){
		"exports missing": func() {},
		"endpoint mismatch": func() {
			s.rig.env = append(s.rig.env, envSSHDockerHost+"=ssh://devsyci@other",
				envWSLDaemonID+"="+testDaemonID)
		},
		"daemon id mismatch": func() {
			s.ExportEnv()
			s.rig.onResult(kindWSLDockerInfo, CommandResult{Stdout: "OTHER:DAEMON:IDXX:YYYY\n"})
		},
		"daemon gone": func() {
			s.ExportEnv()
			s.rig.onResult(kindWSLDockerInfo, exitWith(1, "down"))
		},
		"other run": func() {
			s.ExportEnv()
			s.rig.env = append(s.rig.env, "GITHUB_RUN_ID=1234")
		},
	}
	for name, arrange := range cases {
		s.Run(name, func() {
			arrange()
			err := s.Preflight()
			s.Require().Error(err)
			s.NotEmpty(codesOf(err))
		})
	}
}

func (s *DockerWSLCleanupSuite) TestPreflightRejectsMissingAndIncompleteState() {
	err := verifyDockerWSLSetup(context.Background(), s.rig.deps, testStateFile)
	s.Equal([]Code{CodeDockerWSLEndpoint}, codesOf(err))

	s.rig.onResult("wsl-install", exitWith(100, "apt"))
	s.Require().Error(s.rig.setup())
	s.ExportEnv()
	err = s.Preflight()
	s.Equal([]Code{CodeDockerWSLEndpoint}, codesOf(err))
	s.Contains(err.Error(), "never completed")
}

func (s *DockerWSLCleanupSuite) TestPreflightIsRegisteredWithE2ERun() {
	s.Equal(
		reflect.ValueOf(verifyDockerWSLSetup).Pointer(),
		reflect.ValueOf(dockerWSLPreflight).Pointer(),
	)
}

func (s *DockerWSLCleanupSuite) TestCommandSurface() {
	root := newRootCommand(s.rig.deps)
	for _, args := range [][]string{{cmdDockerWSL, cmdSetup}, {cmdDockerWSL, stageCleanup}} {
		cmd, _, err := root.Find(args)
		s.Require().NoError(err)
		s.Equal(args[1], cmd.Name())
		s.NotNil(cmd.Flags().Lookup("state-file"))
		s.NotNil(cmd.Flags().Lookup("timeout"))
	}
	setup, _, _ := root.Find([]string{cmdDockerWSL, cmdSetup})
	s.Equal("4m0s", setup.Flags().Lookup("timeout").DefValue)
	s.NotNil(setup.Flags().Lookup("endpoint-mode"))
	cleanup, _, _ := root.Find([]string{cmdDockerWSL, stageCleanup})
	s.Equal("2m0s", cleanup.Flags().Lookup("timeout").DefValue)
}

func (s *DockerWSLCleanupSuite) TestCommandsFailExplicitlyOffWindows() {
	rig := newTestRig(goosLinux, []string{envRunnerTemp})
	for _, sub := range []string{"setup", "cleanup"} {
		root := newRootCommand(rig.Dependencies)
		root.SetArgs([]string{cmdDockerWSL, sub})
		err := root.ExecuteContext(context.Background())
		s.Equal([]Code{CodeProfileUnsupported}, codesOf(err), sub)
	}
	s.Empty(rig.runner.calls)
}
