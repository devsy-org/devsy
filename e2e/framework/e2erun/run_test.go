package e2erun

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/e2e/framework/dockerwsl"
	"github.com/devsy-org/devsy/e2e/framework/runenv"
	"github.com/devsy-org/devsy/e2e/framework/runenv/runenvtest"
	"github.com/stretchr/testify/suite"
)

type E2ERunSuite struct {
	suite.Suite
	dir string
}

func TestE2ERunSuite(t *testing.T) { suite.Run(t, new(E2ERunSuite)) }

func (s *E2ERunSuite) SetupTest() { s.dir = s.T().TempDir() }

func (s *E2ERunSuite) TestLegacyLinuxSudoWhitelist() {
	rig := s.rig(runenv.GoosLinux, linuxEnv)
	plan, err := buildE2EPlan(rig.Dependencies, s.options())
	s.Require().NoError(err)

	s.Equal("sudo", plan.suite.Path)
	want := []string{
		envGHUser,
		"GH_ACCESS_TOKEN=ghs_abcdefghijklmnopqrstuvwxyz0123",
		"GH_CREDENTIAL_USERNAME=x-access-token",
		"KUBECONFIG=" + filepath.Join("/home/runner", ".kube", "config"),
		"PATH=/usr/bin:/bin",
		s.binary(),
	}
	want = append(want, s.tailArgs(s.report(), "2")...)
	s.Equal(want, plan.suite.Args)
	s.NotContains(plan.suite.Args, "-E")
	s.Empty(plan.suite.Env)
	s.Empty(plan.preflight)
	s.Equal(900*time.Second+suiteKillGrace, plan.suite.Timeout)
}

func (s *E2ERunSuite) TestLegacyLinuxForwardsDockerHostAndKubeconfig() {
	env := append(
		slices.Clone(linuxEnv),
		"DOCKER_HOST=unix:///run/docker.sock",
		"KUBECONFIG=/k/cfg",
	)
	rig := s.rig(runenv.GoosLinux, env)
	plan, err := buildE2EPlan(rig.Dependencies, s.options())
	s.Require().NoError(err)

	s.Contains(plan.suite.Args, "KUBECONFIG=/k/cfg")
	idx := slices.Index(plan.suite.Args, "DOCKER_HOST=unix:///run/docker.sock")
	s.Require().GreaterOrEqual(idx, 0)
	s.Equal("PATH=/usr/bin:/bin", plan.suite.Args[idx+1])
}

func (s *E2ERunSuite) TestLegacyLinuxRootlessRunsAsUserAfterPodmanInfo() {
	rig := s.rig(runenv.GoosLinux, linuxEnv)
	opts := s.options()
	opts.PodmanMode = podmanModeRootless
	plan, err := buildE2EPlan(rig.Dependencies, opts)
	s.Require().NoError(err)

	s.Equal(s.binary(), plan.suite.Path, "no sudo")
	s.Equal(s.tailArgs(s.report(), "2"), plan.suite.Args)
	s.Equal([]string{"id", cmdPodman}, []string{plan.preflight[0].Path, plan.preflight[1].Path})
	s.Equal([]string{"info"}, plan.preflight[1].Args)
	s.Contains(plan.notes, "XDG_RUNTIME_DIR=/run/user/1001")
	s.Contains(plan.suite.Env, envGHUser)
	s.NotContains(strings.Join(plan.suite.Env, " "), "DOCKER_HOST")
}

func (s *E2ERunSuite) TestRootlessProfileMatchesLegacyRootless() {
	rig := s.rig(runenv.GoosLinux, linuxEnv)
	legacy := s.options()
	legacy.PodmanMode = podmanModeRootless
	lp, err := buildE2EPlan(rig.Dependencies, legacy)
	s.Require().NoError(err)

	profile := s.options()
	profile.Profile = profileLinuxPodmanRootless
	pp, err := buildE2EPlan(rig.Dependencies, profile)
	s.Require().NoError(err)
	s.Equal(lp, pp)
}

func (s *E2ERunSuite) TestLegacyWindowsKeepsPodmanPipe() {
	env := []string{`Path=C:\bin`, "HOME=C:\\Users\\r", envGHUser, runenvtest.EnvRunnerTemp}
	rig := s.rig(runenv.GoosWindows, env)
	plan, err := buildE2EPlan(rig.Dependencies, s.options())
	s.Require().NoError(err)

	s.Equal(s.binary(), plan.suite.Path)
	s.Contains(plan.suite.Env, "DOCKER_HOST="+legacyWindowsDockerHost)
	s.Contains(plan.suite.Env, `PATH=C:\bin`, "case-insensitive lookup of Path")
	s.Contains(plan.suite.Env, envGHUser)
	s.NotContains(strings.Join(plan.suite.Env, " "), "DOCKER_CONTEXT")
	s.Empty(plan.stateFile)
}

func (s *E2ERunSuite) TestDockerWSLProfile() {
	rig := s.rig(runenv.GoosWindows, []string{runenvtest.EnvRunnerTemp})
	plan, err := buildE2EPlan(rig.Dependencies, s.wslOptions())
	s.Require().NoError(err)

	s.Equal(s.binary(), plan.suite.Path)
	s.Equal(
		[]string{"DOCKER_HOST=" + dockerwsl.DeadDockerHost, runenvtest.EnvDockerContextEmpty},
		plan.suite.Env,
	)
	s.Equal(filepath.Join("/tmp/rt", "devsy-ci", "docker-wsl-state.json"), plan.stateFile)
	s.Equal(dockerwsl.RequiredSpecs, plan.expect.Required)
	s.Equal(buildkitLabel, plan.expect.Label)
	s.Contains(plan.suite.Args, "-ginkgo.flake-attempts=1")
	s.Contains(plan.suite.Args, "--ginkgo.label-filter="+buildkitLabel)
}

func (s *E2ERunSuite) TestDefaultBinaryPerOS() {
	rig := s.rig(runenv.GoosWindows, nil)
	abs, err := filepath.Abs("./e2e.test.exe")
	s.Require().NoError(err)
	rig.FS.Data[abs] = []byte("x")

	got, err := resolveBinary(rig.Dependencies, "")
	s.Require().NoError(err)
	s.Equal(abs, got)
}

func (s *E2ERunSuite) TestPlanRejections() {
	for _, tc := range rejectionCases(s.dir) {
		s.Run(tc.name, func() {
			rig := s.rig(tc.goos, tc.env)
			opts := s.options()
			tc.mutate(&opts)

			err := s.run(rig, opts)
			se, ok := errors.AsType[*runenv.StageError](err)
			s.Require().True(ok, "err: %v", err)
			s.Equal(tc.code, se.Code)
			s.Equal(runenv.ExitCodeUsage, runenv.ExitCodeForError(err))
			s.Empty(rig.Runner.Calls, "nothing may run on a rejected configuration")
		})
	}
}

func (s *E2ERunSuite) TestRunDockerWSLGreen() {
	rig, opts := s.wslRig()
	rig.Runner.Handle = s.suiteResult(rig.FS, fixtureValid4, runenv.CommandResult{})
	rig.FS.Data[s.report()] = []byte("stale")

	s.Require().NoError(s.run(rig, opts))
	s.Contains(rig.Output.String(), "4 executed (4 passed)")
	s.True(rig.FS.Has(strings.TrimSuffix(s.report(), ".json") + ".summary.txt"))
	s.Len(rig.Runner.Calls, 1)
}

func (s *E2ERunSuite) TestRunChildNonzeroWithGreenJSON() {
	rig, opts := s.wslRig()
	rig.Runner.Handle = s.suiteResult(
		rig.FS, fixtureValid4, runenv.CommandResult{ExitCode: 1, Stderr: "boom"})

	err := s.run(rig, opts)
	s.Equal([]runenv.Code{runenv.CodeE2ETestFailed}, runenvtest.CodesOf(err))
	s.Equal(runenv.ExitCodeFailure, runenv.ExitCodeForError(err))
	s.Contains(err.Error(), "exit=1")
	s.Contains(err.Error(), "boom")
}

func (s *E2ERunSuite) TestRunExitZeroButReportProblems() {
	for _, tc := range []struct{ fixture, part string }{
		{"", "report not produced"},
		{"zero_executed.json", msgNoExecuted},
		{"missing_required.json", "required spec missing"},
		{"malformed.json", "not a Ginkgo JSON report"},
		{"empty.json", "report file is empty"},
	} {
		s.Run(tc.fixture+tc.part, func() {
			rig, opts := s.wslRig()
			rig.Runner.Handle = s.suiteResult(rig.FS, tc.fixture, runenv.CommandResult{})
			rig.FS.Data[s.report()] = readFixture(s.T(), fixtureValid4) // stale green

			err := s.run(rig, opts)
			s.Equal([]runenv.Code{runenv.CodeE2EReportInvalid}, runenvtest.CodesOf(err))
			s.Contains(err.Error(), tc.part)
			s.Contains(err.Error(), "artifact="+s.report())
		})
	}
}

func (s *E2ERunSuite) TestRunTimeoutAndMissingReportAreBothReported() {
	rig, opts := s.wslRig()
	rig.Runner.Handle = s.suiteResult(
		rig.FS,
		"",
		runenv.CommandResult{TimedOut: true, ExitCode: -1},
	)

	err := s.run(rig, opts)
	s.Equal(
		[]runenv.Code{runenv.CodeE2ETestTimeout, runenv.CodeE2EReportInvalid},
		runenvtest.CodesOf(err),
	)
}

func (s *E2ERunSuite) TestRunGoTestTimeoutPanicIsTimeout() {
	rig, opts := s.wslRig()
	rig.Runner.Handle = s.suiteResult(rig.FS, "failed.json", runenv.CommandResult{
		ExitCode: 2, Stderr: "panic: test timed out after 15m0s",
	})

	err := s.run(rig, opts)
	s.Equal(
		[]runenv.Code{runenv.CodeE2ETestTimeout, runenv.CodeE2EReportInvalid},
		runenvtest.CodesOf(err),
	)
}

func (s *E2ERunSuite) TestRunStartFailure() {
	rig, opts := s.wslRig()
	rig.Runner.Handle = func(runenv.Command) (runenv.CommandResult, error) {
		return runenv.CommandResult{ExitCode: -1}, errors.New("exec format error")
	}

	err := s.run(rig, opts)
	s.Equal(
		[]runenv.Code{runenv.CodeE2ETestFailed, runenv.CodeE2EReportInvalid},
		runenvtest.CodesOf(err),
	)
	s.ErrorContains(err, "exec format error")
}

func (s *E2ERunSuite) TestDockerWSLFailsClosedWithoutVerifier() {
	rig := s.rig(runenv.GoosWindows, []string{runenvtest.EnvRunnerTemp})

	err := runE2E(context.Background(), rig.Dependencies, nil, s.wslOptions())
	s.Equal([]runenv.Code{runenv.CodeDockerWSLEndpoint}, runenvtest.CodesOf(err))
	s.Empty(rig.Runner.Calls, "suite must not run")
}

func (s *E2ERunSuite) TestWSLPreflightErrorStopsSuite() {
	rig, opts := s.wslRig()
	failing := func(context.Context, *runenv.Dependencies, string) error {
		return runenv.NewStageError(
			runenv.CodeDockerWSLOracle,
			"preflight",
			errors.New("state incomplete"),
		)
	}

	err := runE2E(context.Background(), rig.Dependencies, failing, opts)
	s.Equal([]runenv.Code{runenv.CodeDockerWSLOracle}, runenvtest.CodesOf(err))
	s.Empty(rig.Runner.Calls)
}

func (s *E2ERunSuite) TestRootlessPodmanInfoFailureSkipsSuite() {
	rig := s.rig(runenv.GoosLinux, linuxEnv)
	rig.Runner.Handle = func(c runenv.Command) (runenv.CommandResult, error) {
		if c.Path == cmdPodman {
			return runenv.CommandResult{
				ExitCode: 125,
				Stderr:   "cannot connect GH_ACCESS_TOKEN=supersecretvalue",
			}, nil
		}
		return runenv.CommandResult{}, nil
	}
	opts := s.options()
	opts.PodmanMode = podmanModeRootless

	err := s.run(rig, opts)
	s.Equal([]runenv.Code{runenv.CodeE2EPreflightFailed}, runenvtest.CodesOf(err))
	s.Contains(err.Error(), "exit=125")
	s.NotContains(err.Error(), "supersecretvalue")
	s.Len(rig.Runner.Calls, 2, "id and podman info only")
	s.Equal(cmdPodman, rig.Runner.Calls[1].Path)
}

func (s *E2ERunSuite) TestRootlessSuccessRunsPreflightThenSuite() {
	rig := s.rig(runenv.GoosLinux, linuxEnv)
	rig.Runner.Handle = s.suiteResult(rig.FS, "real_ginkgo_report.json", runenv.CommandResult{})
	opts := s.options()
	opts.Label, opts.PodmanMode = buildkitLabel, podmanModeRootless

	s.Require().NoError(s.run(rig, opts))
	paths := make([]string, 0, len(rig.Runner.Calls))
	for _, c := range rig.Runner.Calls {
		paths = append(paths, c.Path)
	}
	s.Equal([]string{"id", cmdPodman, s.binary()}, paths)
}

var linuxEnv = []string{
	"HOME=/home/runner", "PATH=/usr/bin:/bin", envGHUser,
	"GH_ACCESS_TOKEN=ghs_" + "abcdefghijklmnopqrstuvwxyz0123",
	"GH_CREDENTIAL_USERNAME=x-access-token",
	"DEVSY_REQUIRE_MICROSANDBOX=false", "XDG_RUNTIME_DIR=/run/user/1001",
}

func (s *E2ERunSuite) binary() string { return filepath.Join(s.dir, "e2e.test") }
func (s *E2ERunSuite) report() string { return filepath.Join(s.dir, "r-report.json") }

func (s *E2ERunSuite) options() e2eOptions {
	return e2eOptions{
		Profile:       profileLegacy,
		Label:         "build",
		PodmanMode:    podmanModeNone,
		Binary:        s.binary(),
		Report:        s.report(),
		Timeout:       900 * time.Second,
		FlakeAttempts: 2,
	}
}

func (s *E2ERunSuite) wslOptions() e2eOptions {
	opts := s.options()
	opts.Profile, opts.Label, opts.FlakeAttempts = profileDockerWSLSSH, buildkitLabel, 1
	return opts
}

// rig builds fakes for goos with the test binary present.
func (s *E2ERunSuite) rig(goos string, env []string) *runenvtest.Rig {
	rig := runenvtest.NewRig(goos, env)
	rig.FS.Data[s.binary()] = []byte("bin")
	return rig
}

// acceptSetup is a SetupVerifier that accepts any state.
func acceptSetup(context.Context, *runenv.Dependencies, string) error { return nil }

// run executes the plan with a verifier that accepts the docker-wsl state.
func (s *E2ERunSuite) run(rig *runenvtest.Rig, opts e2eOptions) error {
	return runE2E(context.Background(), rig.Dependencies, acceptSetup, opts)
}

// wslRig is a Windows rig for the docker-wsl profile.
func (s *E2ERunSuite) wslRig() (*runenvtest.Rig, e2eOptions) {
	return s.rig(runenv.GoosWindows, []string{runenvtest.EnvRunnerTemp}), s.wslOptions()
}

func (s *E2ERunSuite) tailArgs(report, flake string) []string {
	return []string{
		"-test.v", "-ginkgo.v", "-test.timeout", "15m0s", "-ginkgo.fail-on-empty",
		"-ginkgo.flake-attempts=" + flake, "--ginkgo.label-filter=build",
		"--ginkgo.json-report=" + report,
	}
}

// suiteResult scripts the fake test binary: it writes the named fixture (if
// any) to the report path and returns res. Preflight commands succeed.
func (s *E2ERunSuite) suiteResult(
	files *runenvtest.MemFiles,
	fixture string,
	res runenv.CommandResult,
) func(runenv.Command) (runenv.CommandResult, error) {
	return func(c runenv.Command) (runenv.CommandResult, error) {
		if c.Path == cmdPodman || c.Path == "id" {
			return runenv.CommandResult{}, nil
		}
		if fixture != "" {
			files.Data[s.report()] = readFixture(s.T(), fixture)
		}
		return res, nil
	}
}

type rejectionCase struct {
	name   string
	goos   string
	env    []string
	mutate func(*e2eOptions)
	code   runenv.Code
}

// selectWSL sets the docker-wsl-ssh profile with the one flake attempt it requires.
func selectWSL(o *e2eOptions) { o.Profile, o.FlakeAttempts = profileDockerWSLSSH, 1 }

// rejectionCases lists option sets that must fail with a typed usage error.
func rejectionCases(dir string) []rejectionCase {
	return slices.Concat(profileRejections(), optionRejections(dir))
}

// profileRejections are profile and host combinations that cannot run.
func profileRejections() []rejectionCase {
	withTemp := []string{runenvtest.EnvRunnerTemp}
	return []rejectionCase{
		{"wsl profile on linux", runenv.GoosLinux, nil, selectWSL, runenv.CodeProfileUnsupported},
		{
			"rootless profile on win", runenv.GoosWindows, nil,
			func(o *e2eOptions) { o.Profile = profileLinuxPodmanRootless },
			runenv.CodeProfileUnsupported,
		},
		{
			"standard profile", runenv.GoosLinux, nil,
			func(o *e2eOptions) { o.Profile = profileStandard }, runenv.CodeProfileUnsupported,
		},
		{"legacy on darwin", "darwin", nil, func(*e2eOptions) {}, runenv.CodeProfileUnsupported},
		{
			"unknown profile", runenv.GoosLinux, nil,
			func(o *e2eOptions) { o.Profile = "bogus" }, runenv.CodeConfigInvalid,
		},
		{"rootless profile rootful", runenv.GoosLinux, nil, func(o *e2eOptions) {
			o.Profile, o.PodmanMode = profileLinuxPodmanRootless, podmanModeRootful
		}, runenv.CodeConfigInvalid},
		{"wsl with podman", runenv.GoosWindows, withTemp, func(o *e2eOptions) {
			selectWSL(o)
			o.PodmanMode = podmanModeTrue
		}, runenv.CodeConfigInvalid},
		{"wsl with retries", runenv.GoosWindows, withTemp, func(o *e2eOptions) {
			selectWSL(o)
			o.FlakeAttempts = 2
		}, runenv.CodeConfigInvalid},
		{
			"legacy windows rootless", runenv.GoosWindows, nil,
			func(o *e2eOptions) { o.PodmanMode = podmanModeRootless }, runenv.CodeConfigInvalid,
		},
		{"wsl without state or temp", runenv.GoosWindows, nil, selectWSL, runenv.CodeConfigInvalid},
	}
}

// optionRejections are malformed flag values, valid on any profile.
func optionRejections(dir string) []rejectionCase {
	return []rejectionCase{
		{
			"bad podman mode", runenv.GoosLinux, nil,
			func(o *e2eOptions) { o.PodmanMode = "maybe" }, runenv.CodeConfigInvalid,
		},
		{
			"label with filter syntax", runenv.GoosLinux, nil,
			func(o *e2eOptions) { o.Label = "a && b" }, runenv.CodeConfigInvalid,
		},
		{
			"empty label",
			runenv.GoosLinux,
			nil,
			func(o *e2eOptions) { o.Label = "" },
			runenv.CodeConfigInvalid,
		},
		{
			"zero timeout",
			runenv.GoosLinux,
			nil,
			func(o *e2eOptions) { o.Timeout = 0 },
			runenv.CodeConfigInvalid,
		},
		{
			"zero flake attempts", runenv.GoosLinux, nil,
			func(o *e2eOptions) { o.FlakeAttempts = 0 }, runenv.CodeConfigInvalid,
		},
		{
			"missing report", runenv.GoosLinux, nil,
			func(o *e2eOptions) { o.Report = " " }, runenv.CodeConfigInvalid,
		},
		{
			"missing binary", runenv.GoosLinux, nil,
			func(o *e2eOptions) { o.Binary = filepath.Join(dir, "nope") }, runenv.CodeConfigInvalid,
		},
	}
}
