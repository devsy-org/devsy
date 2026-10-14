package main

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type E2ERunSuite struct {
	suite.Suite
	dir string
}

func TestE2ERunSuite(t *testing.T) { suite.Run(t, new(E2ERunSuite)) }

func (s *E2ERunSuite) SetupTest() { s.dir = s.T().TempDir() }

func (s *E2ERunSuite) TestLegacyLinuxSudoWhitelist() {
	rig := s.rig(goosLinux, linuxEnv)
	plan, err := buildE2EPlan(rig.Dependencies, s.options())
	s.Require().NoError(err)

	s.Equal("sudo", plan.suite.Path)
	want := []string{
		"DEVSY_REQUIRE_MICROSANDBOX=false",
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
	rig := s.rig(goosLinux, env)
	plan, err := buildE2EPlan(rig.Dependencies, s.options())
	s.Require().NoError(err)

	s.Contains(plan.suite.Args, "KUBECONFIG=/k/cfg")
	idx := slices.Index(plan.suite.Args, "DOCKER_HOST=unix:///run/docker.sock")
	s.Require().GreaterOrEqual(idx, 0)
	s.Equal("PATH=/usr/bin:/bin", plan.suite.Args[idx+1])
}

func (s *E2ERunSuite) TestLegacyLinuxRootlessRunsAsUserAfterPodmanInfo() {
	rig := s.rig(goosLinux, linuxEnv)
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
	rig := s.rig(goosLinux, linuxEnv)
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
	env := []string{`Path=C:\bin`, "HOME=C:\\Users\\r", envGHUser, envRunnerTemp}
	rig := s.rig(goosWindows, env)
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
	rig := s.rig(goosWindows, []string{envRunnerTemp})
	plan, err := buildE2EPlan(rig.Dependencies, s.wslOptions())
	s.Require().NoError(err)

	s.Equal(s.binary(), plan.suite.Path)
	s.Equal([]string{"DOCKER_HOST=" + deadDockerHost, envDockerContextEmpty}, plan.suite.Env)
	s.Equal(filepath.Join("/tmp/rt", "devsy-ci", "docker-wsl-state.json"), plan.stateFile)
	s.Equal(dockerWSLRequiredSpecs, plan.expect.Required)
	s.Equal(buildkitLabel, plan.expect.Label)
	s.Contains(plan.suite.Args, "-ginkgo.flake-attempts=1")
	s.Contains(plan.suite.Args, "--ginkgo.label-filter="+buildkitLabel)
}

func (s *E2ERunSuite) TestDefaultBinaryPerOS() {
	rig := s.rig(goosWindows, nil)
	abs, err := filepath.Abs("./e2e.test.exe")
	s.Require().NoError(err)
	rig.files.files[abs] = []byte("x")

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

			err := runE2E(context.Background(), rig.Dependencies, opts)
			se, ok := errors.AsType[*StageError](err)
			s.Require().True(ok, "err: %v", err)
			s.Equal(tc.code, se.Code)
			s.Equal(exitCodeUsage, exitCodeForError(err))
			s.Empty(rig.runner.calls, "nothing may run on a rejected configuration")
		})
	}
}

func (s *E2ERunSuite) TestRunDockerWSLGreen() {
	rig, opts := s.wslRig()
	rig.runner.handle = s.suiteResult(rig.files, fixtureValid4, CommandResult{})
	rig.files.files[s.report()] = []byte("stale")

	s.Require().NoError(runE2E(context.Background(), rig.Dependencies, opts))
	s.Contains(rig.out.String(), "4 executed (4 passed)")
	s.True(rig.files.has(strings.TrimSuffix(s.report(), ".json") + ".summary.txt"))
	s.Len(rig.runner.calls, 1)
}

func (s *E2ERunSuite) TestRunChildNonzeroWithGreenJSON() {
	rig, opts := s.wslRig()
	rig.runner.handle = s.suiteResult(
		rig.files, fixtureValid4, CommandResult{ExitCode: 1, Stderr: "boom"})

	err := runE2E(context.Background(), rig.Dependencies, opts)
	s.Equal([]Code{CodeE2ETestFailed}, codesOf(err))
	s.Equal(exitCodeFailure, exitCodeForError(err))
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
			rig.runner.handle = s.suiteResult(rig.files, tc.fixture, CommandResult{})
			rig.files.files[s.report()] = readFixture(s.T(), fixtureValid4) // stale green

			err := runE2E(context.Background(), rig.Dependencies, opts)
			s.Equal([]Code{CodeE2EReportInvalid}, codesOf(err))
			s.Contains(err.Error(), tc.part)
			s.Contains(err.Error(), "artifact="+s.report())
		})
	}
}

func (s *E2ERunSuite) TestRunTimeoutAndMissingReportAreBothReported() {
	rig, opts := s.wslRig()
	rig.runner.handle = s.suiteResult(rig.files, "", CommandResult{TimedOut: true, ExitCode: -1})

	err := runE2E(context.Background(), rig.Dependencies, opts)
	s.Equal([]Code{CodeE2ETestTimeout, CodeE2EReportInvalid}, codesOf(err))
}

func (s *E2ERunSuite) TestRunGoTestTimeoutPanicIsTimeout() {
	rig, opts := s.wslRig()
	rig.runner.handle = s.suiteResult(rig.files, "failed.json", CommandResult{
		ExitCode: 2, Stderr: "panic: test timed out after 15m0s",
	})

	err := runE2E(context.Background(), rig.Dependencies, opts)
	s.Equal([]Code{CodeE2ETestTimeout, CodeE2EReportInvalid}, codesOf(err))
}

func (s *E2ERunSuite) TestRunStartFailure() {
	rig, opts := s.wslRig()
	rig.runner.handle = func(Command) (CommandResult, error) {
		return CommandResult{ExitCode: -1}, errors.New("exec format error")
	}

	err := runE2E(context.Background(), rig.Dependencies, opts)
	s.Equal([]Code{CodeE2ETestFailed, CodeE2EReportInvalid}, codesOf(err))
	s.ErrorContains(err, "exec format error")
}

func (s *E2ERunSuite) TestDockerWSLFailsClosedWithoutVerifier() {
	rig := s.rig(goosWindows, []string{envRunnerTemp})

	err := runE2E(context.Background(), rig.Dependencies, s.wslOptions())
	s.Equal([]Code{CodeDockerWSLEndpoint}, codesOf(err))
	s.Empty(rig.runner.calls, "suite must not run")
}

func (s *E2ERunSuite) TestWSLPreflightErrorStopsSuite() {
	rig, opts := s.wslRig()
	dockerWSLPreflight = func(context.Context, *Dependencies, string) error {
		return newStageError(CodeDockerWSLOracle, "preflight", errors.New("state incomplete"))
	}

	err := runE2E(context.Background(), rig.Dependencies, opts)
	s.Equal([]Code{CodeDockerWSLOracle}, codesOf(err))
	s.Empty(rig.runner.calls)
}

func (s *E2ERunSuite) TestRootlessPodmanInfoFailureSkipsSuite() {
	rig := s.rig(goosLinux, linuxEnv)
	rig.runner.handle = func(c Command) (CommandResult, error) {
		if c.Path == cmdPodman {
			return CommandResult{
				ExitCode: 125,
				Stderr:   "cannot connect GH_ACCESS_TOKEN=supersecretvalue",
			}, nil
		}
		return CommandResult{}, nil
	}
	opts := s.options()
	opts.PodmanMode = podmanModeRootless

	err := runE2E(context.Background(), rig.Dependencies, opts)
	s.Equal([]Code{CodeE2EPreflightFailed}, codesOf(err))
	s.Contains(err.Error(), "exit=125")
	s.NotContains(err.Error(), "supersecretvalue")
	s.Len(rig.runner.calls, 2, "id and podman info only")
	s.Equal(cmdPodman, rig.runner.calls[1].Path)
}

func (s *E2ERunSuite) TestRootlessSuccessRunsPreflightThenSuite() {
	rig := s.rig(goosLinux, linuxEnv)
	rig.runner.handle = s.suiteResult(rig.files, "real_ginkgo_report.json", CommandResult{})
	opts := s.options()
	opts.Label, opts.PodmanMode = buildkitLabel, podmanModeRootless

	s.Require().NoError(runE2E(context.Background(), rig.Dependencies, opts))
	paths := make([]string, 0, len(rig.runner.calls))
	for _, c := range rig.runner.calls {
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
func (s *E2ERunSuite) rig(goos string, env []string) *testRig {
	rig := newTestRig(goos, env)
	rig.files.files[s.binary()] = []byte("bin")
	return rig
}

// wslRig is a Windows rig with a passing docker-wsl state verifier, restored
// when the test ends.
func (s *E2ERunSuite) wslRig() (*testRig, e2eOptions) {
	prev := dockerWSLPreflight
	s.T().Cleanup(func() { dockerWSLPreflight = prev })
	dockerWSLPreflight = func(context.Context, *Dependencies, string) error { return nil }
	return s.rig(goosWindows, []string{envRunnerTemp}), s.wslOptions()
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
	files *memFiles,
	fixture string,
	res CommandResult,
) func(Command) (CommandResult, error) {
	return func(c Command) (CommandResult, error) {
		if c.Path == cmdPodman || c.Path == "id" {
			return CommandResult{}, nil
		}
		if fixture != "" {
			files.files[s.report()] = readFixture(s.T(), fixture)
		}
		return res, nil
	}
}

func codesOf(err error) []Code {
	var codes []Code
	var walk func(error)
	walk = func(e error) {
		if e == nil {
			return
		}
		if se, ok := e.(*StageError); ok {
			codes = append(codes, se.Code)
			return
		}
		if multi, ok := e.(interface{ Unwrap() []error }); ok {
			for _, inner := range multi.Unwrap() {
				walk(inner)
			}
		}
	}
	walk(err)
	return codes
}

type rejectionCase struct {
	name   string
	goos   string
	env    []string
	mutate func(*e2eOptions)
	code   Code
}

// rejectionCases lists option sets that must fail with a typed usage error.
func rejectionCases(dir string) []rejectionCase {
	wsl := func(o *e2eOptions) { o.Profile, o.FlakeAttempts = profileDockerWSLSSH, 1 }
	withTemp := []string{envRunnerTemp}
	return []rejectionCase{
		{"wsl profile on linux", goosLinux, nil, wsl, CodeProfileUnsupported},
		{
			"rootless profile on win", goosWindows, nil,
			func(o *e2eOptions) { o.Profile = profileLinuxPodmanRootless }, CodeProfileUnsupported,
		},
		{
			"standard profile", goosLinux, nil,
			func(o *e2eOptions) { o.Profile = profileStandard }, CodeProfileUnsupported,
		},
		{"legacy on darwin", "darwin", nil, func(*e2eOptions) {}, CodeProfileUnsupported},
		{
			"unknown profile", goosLinux, nil,
			func(o *e2eOptions) { o.Profile = "bogus" }, CodeConfigInvalid,
		},
		{"rootless profile rootful", goosLinux, nil, func(o *e2eOptions) {
			o.Profile, o.PodmanMode = profileLinuxPodmanRootless, podmanModeRootful
		}, CodeConfigInvalid},
		{"wsl with podman", goosWindows, withTemp, func(o *e2eOptions) {
			wsl(o)
			o.PodmanMode = podmanModeTrue
		}, CodeConfigInvalid},
		{"wsl with retries", goosWindows, withTemp, func(o *e2eOptions) {
			wsl(o)
			o.FlakeAttempts = 2
		}, CodeConfigInvalid},
		{
			"legacy windows rootless", goosWindows, nil,
			func(o *e2eOptions) { o.PodmanMode = podmanModeRootless }, CodeConfigInvalid,
		},
		{
			"bad podman mode", goosLinux, nil,
			func(o *e2eOptions) { o.PodmanMode = "maybe" }, CodeConfigInvalid,
		},
		{
			"label with filter syntax", goosLinux, nil,
			func(o *e2eOptions) { o.Label = "a && b" }, CodeConfigInvalid,
		},
		{"empty label", goosLinux, nil, func(o *e2eOptions) { o.Label = "" }, CodeConfigInvalid},
		{"zero timeout", goosLinux, nil, func(o *e2eOptions) { o.Timeout = 0 }, CodeConfigInvalid},
		{
			"zero flake attempts", goosLinux, nil,
			func(o *e2eOptions) { o.FlakeAttempts = 0 }, CodeConfigInvalid,
		},
		{
			"missing report", goosLinux, nil,
			func(o *e2eOptions) { o.Report = " " }, CodeConfigInvalid,
		},
		{
			"missing binary", goosLinux, nil,
			func(o *e2eOptions) { o.Binary = filepath.Join(dir, "nope") }, CodeConfigInvalid,
		},
		{"wsl without state or temp", goosWindows, nil, wsl, CodeConfigInvalid},
	}
}
