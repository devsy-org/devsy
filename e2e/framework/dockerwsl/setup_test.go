package dockerwsl

import (
	"context"
	"encoding/base64"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devsy-org/devsy/e2e/framework/runenv"
	"github.com/devsy-org/devsy/e2e/framework/runenv/runenvtest"
	"github.com/stretchr/testify/suite"
)

const (
	originalSSHConfig = "Host keep\r\n  User me\r\n"
	wantEndpoint      = "ssh://devsyci@" + testAlias
	envLineSSHHost    = envSSHDockerHost + "=" + wantEndpoint + "\n"
	envLineDaemon     = envWSLDaemonID + "=" + testDaemonID + "\n"
)

type DockerWSLSetupSuite struct {
	suite.Suite
	rig *wslRig
}

func TestDockerWSLSetupSuite(t *testing.T) { suite.Run(t, new(DockerWSLSetupSuite)) }

func (s *DockerWSLSetupSuite) SetupTest() {
	s.rig = newWSLRig(s.T())
	s.rig.files.Data[testUserConfig] = []byte(originalSSHConfig)
}

func (s *DockerWSLSetupSuite) Exports() string { return s.rig.files.Text(testGHEnv) }

func (s *DockerWSLSetupSuite) TestHappyPathProvesThenExports() {
	var pidfileAtLaunch string
	s.rig.host.onStart = func([]string) {
		if pidfileAtLaunch == "" {
			pidfileAtLaunch = s.rig.state().Owned.DockerPidfile
		}
	}

	s.Require().NoError(s.rig.setup())

	st := s.rig.state()
	s.Equal(stageVerified, st.Stage)
	s.Equal(testDaemonID, st.DaemonID)
	s.Equal(wantEndpoint, st.SSHEndpoint)
	s.Equal(testAlias, st.SSHHostAlias)
	s.Equal(dockerPidfile, pidfileAtLaunch, "dockerd is registered before it is launched")
	s.Contains(st.Packages, "docker-ce=5:29.9.0-1")
	env := s.Exports()
	s.Contains(env, envLineSSHHost)
	s.Contains(env, envLineDaemon)
	s.Contains(env, "DOCKER_CONFIG="+fixtureFile("docker-config")+"\n")
	s.Equal(
		fixtureFile("docker-cli", "docker")+"\n",
		s.rig.files.Text(testGHPath),
		"directory, not exe path",
	)
}

func (s *DockerWSLSetupSuite) TestHappyPathWiresSSHAndDockerExactly() {
	s.Require().NoError(s.rig.setup())

	cfg := s.rig.files.Text(testUserConfig)
	s.True(strings.HasPrefix(cfg, sshBlockBegin(testRunID)))
	s.True(strings.HasSuffix(cfg, "\n"+originalSSHConfig), "original bytes follow the block")
	s.Contains(cfg, "HostName 127.0.0.1")
	s.Contains(cfg, "StrictHostKeyChecking yes")
	known := s.rig.files.Text(fixtureFile("known_hosts"))
	s.Contains(known, testAlias+" ssh-ed25519 ")
	s.Contains(known, "["+testAlias+"]:2222 ssh-ed25519 ")
	s.Equal(originalSSHConfig, s.rig.files.Text(fixtureFile("ssh-config-original")))
	s.Equal(1, s.rig.runner.count("icacls-save"), "existing config ACLs are saved")
	s.Equal(1, s.rig.runner.count("icacls-restrict"))
	s.Equal(1, s.rig.runner.count(kindDockerInfoDead))
	s.AssertDockerEnvironment()
	s.AssertDetachedLaunches()
}

func (s *DockerWSLSetupSuite) AssertDockerEnvironment() {
	for i, kind := range s.rig.runner.kinds {
		if kind != kindDockerInfoSSH && kind != kindDockerInfoDead {
			continue
		}
		env := s.rig.runner.calls[i].Env
		s.Contains(env, "DOCKER_CONTEXT=", "context is cleared for every oracle")
		s.Contains(env, "DOCKER_CONFIG="+fixtureFile("docker-config"))
	}
	for i, kind := range s.rig.runner.kinds {
		if kind == kindDockerInfoSSH {
			s.Contains(s.rig.runner.calls[i].Env, "DOCKER_HOST="+wantEndpoint)
		}
	}
}

func (s *DockerWSLSetupSuite) AssertDetachedLaunches() {
	s.Require().Len(s.rig.host.started, 2)
	dockerd, sshd := strings.Join(
		s.rig.host.started[0],
		" ",
	), strings.Join(
		s.rig.host.started[1],
		" ",
	)
	s.Contains(dockerd, "-d Ubuntu-24.04 -u root --exec /bin/sh -c "+dockerdLaunchScript)
	s.Contains(dockerd, dockerdMarker)
	s.Contains(sshd, sshdConfigFile)
	all := s.rig.allArgs() + dockerd + sshd
	s.NotContains(all, "2375")
	s.NotContains(all, "pkill")
	s.NotContains(all, testPrivateKey)
	s.NotContains(all, "tcp://")
}

func (s *DockerWSLSetupSuite) TestAuthorizedKeyIsThePublicHalfOnly() {
	s.Require().NoError(s.rig.setup())

	pub := strings.TrimSpace(s.rig.files.Text(fixtureFile("id_ed25519.pub")))
	pubKey, err := parseHostKey(pub)
	s.Require().NoError(err)
	want := base64.StdEncoding.EncodeToString([]byte(pubKey))
	var found bool
	for i, kind := range s.rig.runner.kinds {
		if kind == "wsl-install-key" {
			found = strings.Contains(
				strings.Join(s.rig.runner.calls[i].Args, " "),
				" "+ciUser+" "+want,
			)
		}
	}
	s.True(found, "install-key receives user and base64 public key")
	s.NotContains(s.rig.out.String()+s.Exports()+s.rig.files.Text(testStateFile), testPrivateKey)
}

// ssh-keygen leaves explicit Administrators and SYSTEM entries that dropping
// inheritance does not remove; setup must delete them rather than fail.
func (s *DockerWSLSetupSuite) TestKeygenACLEntriesOfOtherPrincipalsAreRemoved() {
	acl := []string{`HOST\runner:(R)`, `BUILTIN\Administrators:(F)`, `NT AUTHORITY\SYSTEM:(F)`}
	s.rig.on(
		"icacls-show",
		func(_ context.Context, c runenv.Command) (runenv.CommandResult, error) {
			return runenv.CommandResult{
				Stdout: c.Args[0] + " " + strings.Join(acl, "\n  ") + "\n",
			}, nil
		},
	)
	s.rig.on(
		"icacls-remove",
		func(_ context.Context, c runenv.Command) (runenv.CommandResult, error) {
			acl = slices.DeleteFunc(acl, func(entry string) bool {
				return strings.HasPrefix(entry, c.Args[2]+":")
			})
			return runenv.CommandResult{}, nil
		},
	)

	s.Require().NoError(s.rig.setup())

	s.Equal(2, s.rig.runner.count("icacls-remove"))
	s.Equal([]string{`HOST\runner:(R)`}, acl)
}

func (s *DockerWSLSetupSuite) TestStageLinesAreStructured() {
	s.Require().NoError(s.rig.setup())
	out := s.rig.out.String()
	for _, stage := range []string{stageValidate, stageDaemon, stageOracles} {
		s.Contains(out, "stage="+stage+" duration=")
	}
	s.Contains(out, "attempt=1 result=ok")
}

func (s *DockerWSLSetupSuite) TestLocalModeNeedsNoGitHubFiles() {
	s.rig.env = []string{
		runenvtest.EnvRunnerTemp, "USERNAME=runner", "USERPROFILE=/home/runneradmin",
	}
	s.Require().
		NoError(s.rig.controller(func(o *dockerWSLOptions) { o.RunID = testRunID }).setup(context.Background()))

	s.Contains(s.rig.out.String(), "local env: "+envLineSSHHost)
	s.Contains(s.rig.out.String(), "local path: "+fixtureFile("docker-cli", "docker"))
	s.Empty(s.rig.files.Text(testGHEnv))
}

func (s *DockerWSLSetupSuite) TestUnsafeExportValuesAreRejected() {
	d := s.rig.controller()
	d.state.Owned.DockerBinDirectory = fixtureFile("bin")
	d.state.Owned.DockerConfigDir = fixtureFile("cfg")
	d.state.SSHEndpoint = wantEndpoint

	err := d.export("id\nEVIL=1")

	s.Require().Error(err)
	s.NotContains(s.Exports(), "EVIL")
	s.NotContains(s.Exports(), "id\n")
}

func (s *DockerWSLSetupSuite) TestDirectEndpointMode() {
	s.Require().NoError(s.rig.controller(func(o *dockerWSLOptions) {
		o.EndpointMode = sshModeDirect
	}).setup(context.Background()))

	st := s.rig.state()
	s.Equal("ssh://devsyci@127.0.0.1:2222", st.SSHEndpoint)
	s.NotContains(s.rig.files.Text(testUserConfig), "HostKeyAlias")
	s.Contains(s.rig.files.Text(fixtureFile("known_hosts")), "[127.0.0.1]:2222 ssh-ed25519 ")
}

func (s *DockerWSLSetupSuite) TestVMAddressFallbackAfterLoopbackFailure() {
	s.rig.dialer.answer["127.0.0.1:2222"] = httpBanner
	s.rig.dialer.answer[testVMIP+":2222"] = testBanner

	s.Require().NoError(s.rig.setup())

	s.Contains(s.rig.files.Text(testUserConfig), "HostName "+testVMIP)
	s.NotContains(strings.Join(s.rig.dialer.dialed, " "), "172.17.0.1", "later candidates untried")
}

func (s *DockerWSLSetupSuite) TestTransientSSHFailureIsRetried() {
	var calls atomic.Int32
	s.rig.on("ssh", func(context.Context, runenv.Command) (runenv.CommandResult, error) {
		if calls.Add(1) < 3 {
			return runenv.CommandResult{ExitCode: 255, Stderr: "Connection refused"}, nil
		}
		return runenv.CommandResult{}, nil
	})
	s.Require().NoError(s.rig.setup())
	s.Equal(int32(3), calls.Load())
}

func (s *DockerWSLSetupSuite) TestRepeatedSetupIsANoOp() {
	s.Require().NoError(s.rig.setup())
	s.rig.runner.reset()
	before := s.rig.files.Text(testStateFile)

	s.Require().NoError(s.rig.setup())

	s.Empty(s.rig.runner.calls)
	s.Equal(before, s.rig.files.Text(testStateFile))
	s.Contains(s.rig.out.String(), "already set up")
}

func (s *DockerWSLSetupSuite) TestSetupRefusesUnfinishedStateOfSameRun() {
	s.rig.onResult("wsl-install", runenv.CommandResult{ExitCode: 100})
	s.Require().Error(s.rig.setup())
	s.rig.runner.reset()
	before := s.rig.files.Text(testStateFile)

	err := s.rig.setup()

	s.Equal([]runenv.Code{runenv.CodeConfigInvalid}, runenvtest.CodesOf(err))
	s.Contains(err.Error(), "run docker-wsl cleanup first")
	s.Empty(s.rig.runner.calls)
	s.Equal(before, s.rig.files.Text(testStateFile))
}

func (s *DockerWSLSetupSuite) TestOtherJobsStateIsNeverReset() {
	other := newDockerWSLState("99-1-e2e", defaultDistribution)
	other.Owned.CIUser = ciUser
	s.Require().NoError(newStateStore(s.rig.files, testStateFile).save(other))
	before := s.rig.files.Text(testStateFile)

	err := s.rig.setup()

	s.Equal([]runenv.Code{runenv.CodeConfigInvalid}, runenvtest.CodesOf(err))
	s.Contains(err.Error(), "belongs to run")
	s.Empty(s.rig.runner.calls)
	s.Equal(before, s.rig.files.Text(testStateFile))
}

func (s *DockerWSLSetupSuite) TestUnsupportedHostIsAnExplicitError() {
	s.rig.deps.GOOS = runenv.GoosLinux
	_, err := newDockerWSL(s.rig.deps, s.rig.host, dockerWSLOptions{})
	s.Equal([]runenv.Code{runenv.CodeProfileUnsupported}, runenvtest.CodesOf(err))
	s.Contains(err.Error(), "native Windows")
}

func (s *DockerWSLSetupSuite) TestOptionValidation() {
	for name, opts := range map[string]dockerWSLOptions{
		"bad distribution": {Distribution: "x; rm"},
		"bad mode":         {EndpointMode: "tcp"},
		"bad run id":       {RunID: "///"},
	} {
		_, err := newDockerWSL(s.rig.deps, s.rig.host, opts)
		s.Equal([]runenv.Code{runenv.CodeConfigInvalid}, runenvtest.CodesOf(err), name)
	}
	s.rig.env = nil
	_, err := newDockerWSL(s.rig.deps, s.rig.host, dockerWSLOptions{})
	s.Equal(
		[]runenv.Code{runenv.CodeConfigInvalid},
		runenvtest.CodesOf(err),
		"no RUNNER_TEMP and no --state-file",
	)
}

func (s *DockerWSLSetupSuite) TestBudgetExhaustionIsATimeout() {
	s.rig.on(
		"wsl-install",
		func(ctx context.Context, _ runenv.Command) (runenv.CommandResult, error) {
			<-ctx.Done()
			return runenv.CommandResult{ExitCode: -1}, ctx.Err()
		},
	)
	d := s.rig.controller(
		func(o *dockerWSLOptions) { o.Timeout = diagnosticsReserve + time.Second },
	)

	err := d.setup(context.Background())

	s.Equal([]runenv.Code{runenv.CodeDockerWSLSetupTimeout}, runenvtest.CodesOf(err))
	s.Contains(err.Error(), "install-packages")
}

func (s *DockerWSLSetupSuite) TestWSLListIsRequestedAsUTF8() {
	s.Require().NoError(s.rig.setup())
	s.Equal([]string{"WSL_UTF8=1"}, s.rig.runner.calls[s.rig.runner.index("wsl-list")].Env)
}

func (s *DockerWSLSetupSuite) TestWSLErrorOutputInUTF16IsDecoded() {
	s.rig.onResult("wsl-list", runenv.CommandResult{
		ExitCode: 1,
		Stderr:   utf16le("Windows Subsystem for Linux has no installed distributions.\r\n"),
	})
	err := s.rig.setup()
	s.Equal([]runenv.Code{runenv.CodeDockerWSLDistroGone}, runenvtest.CodesOf(err))
	s.Contains(err.Error(), "no installed distributions")
	s.NotContains(err.Error(), "\x00")
}

func (s *DockerWSLSetupSuite) TestNoTemporaryStateRemainsAfterSetup() {
	s.Require().NoError(s.rig.setup())
	for _, name := range s.rig.files.Writes {
		s.NotContains(name, ".tmp-", "state goes through the atomic FileStore path")
	}
	s.Contains(s.rig.files.Writes, testStateFile)
}
