package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/onsi/ginkgo/v2/types"
	"github.com/stretchr/testify/suite"
)

const (
	e2eTestToken   = "tok-secret-value"
	e2eTestDocker  = "unix:///var/run/docker.sock"
	e2eTestTarget  = "integration-x"
	e2eTestBinPath = "./e2e.test"
	e2eTestTimeout = "1500s"
	e2eCmdPodman   = "podman"
	e2eCmdPrepare  = "prepare"
	e2eCmdRun      = "run"
	e2eFlagLabel   = "--label"
)

var errE2EBoom = errors.New("boom")

// e2eFakeRunner records every command and fails the names listed in errs.
type e2eFakeRunner struct {
	specs []commandSpec
	errs  map[string]error
}

func (f *e2eFakeRunner) Run(_ context.Context, spec commandSpec) error {
	f.specs = append(f.specs, spec)
	return f.errs[spec.Name]
}

func (f *e2eFakeRunner) names() []string {
	out := make([]string, 0, len(f.specs))
	for _, spec := range f.specs {
		out = append(out, spec.Name)
	}
	return out
}

func e2eNoFocusedChecks(context.Context, commandRunner, Suite, e2eOptions) error {
	return nil
}

func e2eEnvValue(env []string, key string) (string, bool) {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v, true
		}
	}
	return "", false
}

func e2eTargetSuite(privileged bool) Suite {
	return Suite{
		Group:         groupIntegration,
		Label:         e2eTestTarget,
		Runner:        "ubuntu-latest",
		Resolved:      privileged,
		TestTimeout:   e2eTestTimeout,
		FlakeAttempts: 1,
	}
}

// e2eSuiteReport returns a Ginkgo report whose specs all carry the target label and passed on
// their first attempt.
func e2eSuiteReport(specs int) types.Report {
	r := types.Report{SuiteSucceeded: true}
	for range specs {
		r.SpecReports = append(r.SpecReports, types.SpecReport{
			LeafNodeType:             types.NodeTypeIt,
			LeafNodeText:             "spec",
			ContainerHierarchyLabels: [][]string{{e2eTestTarget}},
			State:                    types.SpecStatePassed,
			NumAttempts:              1,
		})
	}
	return r
}

func e2eWantTestArgs(timeout string, flakes int) []string {
	return []string{
		"-test.v",
		"-ginkgo.v",
		"-test.timeout", timeout,
		"-ginkgo.fail-on-empty",
		fmt.Sprintf("-ginkgo.flake-attempts=%d", flakes),
		"--ginkgo.label-filter=" + e2eTestTarget,
	}
}

// E2ECommandSuite covers the `ci e2e` prepare and run subcommands.
type E2ECommandSuite struct {
	suite.Suite

	dir     string
	fake    *e2eFakeRunner
	env     map[string]string
	report  []byte
	readErr error
	reads   []string
	out     bytes.Buffer
	errOut  bytes.Buffer
}

func TestE2ECommandSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(E2ECommandSuite))
}

func (s *E2ECommandSuite) SetupTest() {
	s.dir = s.T().TempDir()
	s.fake = &e2eFakeRunner{}
	s.env = map[string]string{
		"GH_USERNAME":            "octo",
		"GH_ACCESS_TOKEN":        e2eTestToken,
		"GH_CREDENTIAL_USERNAME": "x-access-token",
		"HOME":                   "/home/runner",
		"PATH":                   "/usr/bin:/bin",
		"XDG_RUNTIME_DIR":        "/run/user/1001",
	}
	s.report = nil
	s.readErr = nil
	s.reads = nil
	s.out.Reset()
	s.errOut.Reset()
}

func (s *E2ECommandSuite) TestPrepareCopiesDevsyBinariesFlat() {
	s.Require().NoError(prepareE2E(&s.out, osLinux, s.binSource(), s.dir))

	entries, err := os.ReadDir(filepath.Join(s.dir, e2eBinDirName))
	s.Require().NoError(err)
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	s.Equal([]string{"devsy-darwin-arm64", "devsy-linux-amd64"}, names)
	s.Contains(s.out.String(), filepath.Join("mac", "notes.txt"))
}

func (s *E2ECommandSuite) TestPrepareSetsExecutableMode() {
	if runtime.GOOS == osWindows {
		s.T().Skip("windows has no unix mode bits")
	}
	s.Require().NoError(prepareE2E(&s.out, osLinux, s.binSource(), s.dir))

	for _, name := range []string{"devsy-linux-amd64", "devsy-darwin-arm64"} {
		info, err := os.Stat(filepath.Join(s.dir, e2eBinDirName, name))
		s.Require().NoError(err)
		s.Equal(os.FileMode(0o755), info.Mode().Perm(), name)
	}
}

func (s *E2ECommandSuite) TestPrepareMarksTestBinaryExecutable() {
	if runtime.GOOS == osWindows {
		s.T().Skip("windows has no unix mode bits")
	}
	testBinary := filepath.Join(s.dir, e2eTestBinary)
	s.writeFile(testBinary, 0o644)

	s.Require().NoError(prepareE2E(&s.out, osLinux, s.binSource(), s.dir))

	info, err := os.Stat(testBinary)
	s.Require().NoError(err)
	s.Equal(os.FileMode(0o755), info.Mode().Perm())
}

func (s *E2ECommandSuite) TestPrepareLeavesTestBinaryAloneOnWindows() {
	testBinary := filepath.Join(s.dir, e2eTestBinary)
	s.writeFile(testBinary, 0o644)
	before, err := os.Stat(testBinary)
	s.Require().NoError(err)

	s.Require().NoError(prepareE2E(&s.out, osWindows, s.binSource(), s.dir))

	after, err := os.Stat(testBinary)
	s.Require().NoError(err)
	s.Equal(before.Mode(), after.Mode())
}

func (s *E2ECommandSuite) TestPrepareWithoutTestBinary() {
	s.Require().NoError(prepareE2E(&s.out, osLinux, s.binSource(), s.dir))
	s.NoFileExists(filepath.Join(s.dir, e2eTestBinary))
}

func (s *E2ECommandSuite) TestPrepareFailsWithoutDevsyBinaries() {
	src := s.T().TempDir()
	s.writeFile(filepath.Join(src, "other-tool"), 0o755)

	err := prepareE2E(&s.out, osLinux, src, s.dir)

	s.ErrorIs(err, errNoDevsyBinaries)
	s.NoDirExists(filepath.Join(s.dir, e2eBinDirName))
}

func (s *E2ECommandSuite) TestCLIPrepareRequiresBinSrc() {
	code := e2eCLI([]string{e2eCmdPrepare}, &s.out, &s.errOut)

	s.Equal(2, code)
	s.Contains(s.errOut.String(), "--bin-src")
}

func (s *E2ECommandSuite) TestCLIPrepareCopiesBinaries() {
	code := e2eCLI(
		[]string{"prepare", "--bin-src", s.binSource(), "--e2e-dir", s.dir},
		&s.out,
		&s.errOut,
	)

	s.Equal(0, code, s.errOut.String())
	s.FileExists(filepath.Join(s.dir, e2eBinDirName, "devsy-darwin-arm64"))
}

func (s *E2ECommandSuite) TestRunLinuxPrivilegedBuildsSudoCommand() {
	s.env["KUBECONFIG"] = "/etc/kube/config"
	s.env["DOCKER_HOST"] = e2eTestDocker

	s.Require().NoError(s.runnerFor(osLinux).run(context.Background(), e2eTargetSuite(true)))

	s.Require().Len(s.fake.specs, 1)
	spec := s.fake.specs[0]
	s.Equal("sudo", spec.Name)
	s.Empty(spec.Env)
	s.Equal(s.dir, spec.Dir)
	env := []string{
		"GH_USERNAME=octo",
		"GH_ACCESS_TOKEN=" + e2eTestToken,
		"GH_CREDENTIAL_USERNAME=x-access-token",
		"KUBECONFIG=/etc/kube/config",
		"PATH=/usr/bin:/bin",
		"DOCKER_HOST=" + e2eTestDocker,
	}
	s.Equal(
		slices.Concat(
			env,
			[]string{e2eTestBinPath},
			e2eWantTestArgs(e2eTestTimeout, 1),
		),
		spec.Args,
	)
}

func (s *E2ECommandSuite) TestRunLinuxPrivilegedOmitsUnsetDockerHost() {
	s.Require().NoError(s.runnerFor(osLinux).run(context.Background(), e2eTargetSuite(true)))

	spec := s.fake.specs[0]
	_, ok := e2eEnvValue(spec.Args, "DOCKER_HOST")
	s.False(ok)
}

func (s *E2ECommandSuite) TestRunDockerWSLUsesWSLDaemonAndClearsContext() {
	target := e2eTargetSuite(false)
	target.Runner = "windows-latest"
	target.Runtime = runtimeDockerWSL
	target.ExpectedSpecCount = 1
	s.env["DOCKER_HOST"] = e2eTestDocker
	s.setReport(e2eSuiteReport(1))

	s.Require().NoError(s.runnerFor(osWindows).run(context.Background(), target))

	spec := s.fake.specs[0]
	host, ok := e2eEnvValue(spec.Env, "DOCKER_HOST")
	s.True(ok)
	s.Equal("npipe:////./pipe/devsy-ci-no-daemon", host)
	s.NotEqual(windowsDockerHost, host)
	dockerContext, ok := e2eEnvValue(spec.Env, "DOCKER_CONTEXT")
	s.True(ok, "DOCKER_CONTEXT must be present so an inherited context is cleared")
	s.Empty(dockerContext)
}

func (s *E2ECommandSuite) TestRunWithoutDockerWSLLeavesDockerContextUnset() {
	s.Require().NoError(s.runnerFor(osLinux).run(context.Background(), e2eTargetSuite(false)))

	_, ok := e2eEnvValue(s.fake.specs[0].Env, "DOCKER_CONTEXT")
	s.False(ok)
}

func (s *E2ECommandSuite) TestJSONReportArgOnlyWithPinnedSpecCount() {
	reportArg := "-ginkgo.json-report=" + e2eTestTarget + "-report.json"
	tests := map[string]struct {
		group string
		count int
		want  bool
	}{
		"pinned in integration":    {group: groupIntegration, count: 2, want: true},
		"unpinned integration":     {group: groupIntegration, count: 0},
		"pinned in microsandbox":   {group: groupMicrosandbox, count: 2},
		"unpinned in unprivileged": {group: groupUnprivileged},
	}
	for name, tc := range tests {
		target := e2eTargetSuite(false)
		target.Group = tc.group
		target.ExpectedSpecCount = tc.count
		want := e2eWantTestArgs(e2eTestTimeout, 1)
		if tc.want {
			want = append(want, reportArg)
		}
		s.Equal(want, e2eTestArgs(target), name)
	}
}

func (s *E2ECommandSuite) TestRunChecksReportBeforePostChecks() {
	target := e2eTargetSuite(false)
	target.ExpectedSpecCount = 2
	target.PostChecks = []string{postCheckDockerInstalled}
	s.setReport(e2eSuiteReport(2))

	s.Require().NoError(s.runnerFor(osLinux).run(context.Background(), target))

	s.Equal([]string{e2eTestBinPath, dockerBinary, dockerBinary}, s.fake.names())
	s.Equal([]string{filepath.Join(s.dir, e2eTestTarget+"-report.json")}, s.reads)
	s.Contains(s.out.String(), "report check passed: 2 specs\n")
}

func (s *E2ECommandSuite) TestRunRejectsReportThatFailsCheck() {
	tests := map[string]func(*types.Report){
		"fewer specs than expected": func(r *types.Report) { r.SpecReports = r.SpecReports[:1] },
		"failed spec":               func(r *types.Report) { r.SpecReports[0].State = types.SpecStateFailed },
		"suite not successful":      func(r *types.Report) { r.SuiteSucceeded = false },
	}
	for name, mutate := range tests {
		s.Run(name, func() {
			s.fake = &e2eFakeRunner{}
			r := e2eSuiteReport(2)
			mutate(&r)
			s.setReport(r)
			target := e2eTargetSuite(false)
			target.ExpectedSpecCount = 2
			target.PostChecks = []string{postCheckDockerInstalled}

			err := s.runnerFor(osLinux).run(context.Background(), target)

			s.ErrorContains(err, "verify report")
			s.Equal([]string{e2eTestBinPath}, s.fake.names(), "post checks must not run")
			s.NotContains(s.out.String(), "report check passed")
		})
	}
}

func (s *E2ECommandSuite) TestRunFailsWhenReportIsUnreadable() {
	target := e2eTargetSuite(false)
	target.ExpectedSpecCount = 2
	target.PostChecks = []string{postCheckDockerInstalled}
	s.readErr = errE2EBoom

	err := s.runnerFor(osLinux).run(context.Background(), target)

	s.ErrorIs(err, errE2EBoom)
	s.Equal([]string{e2eTestBinPath}, s.fake.names())
}

func (s *E2ECommandSuite) TestRunSkipsReportWithoutPinnedSpecCount() {
	s.Require().NoError(s.runnerFor(osLinux).run(context.Background(), e2eTargetSuite(false)))

	s.Empty(s.reads)
	s.NotContains(s.out.String(), "report check")
}

func (s *E2ECommandSuite) TestRunUnprivilegedRunsDirectly() {
	s.Require().NoError(s.runnerFor(osLinux).run(context.Background(), e2eTargetSuite(false)))

	s.Require().Len(s.fake.specs, 1)
	spec := s.fake.specs[0]
	s.Equal(e2eTestBinPath, spec.Name)
	s.Equal(s.dir, spec.Dir)
	s.Equal(e2eWantTestArgs(e2eTestTimeout, 1), spec.Args)
	token, ok := e2eEnvValue(spec.Env, "GH_ACCESS_TOKEN")
	s.True(ok)
	s.Equal(e2eTestToken, token)
	kubeconfig, ok := e2eEnvValue(spec.Env, "KUBECONFIG")
	s.True(ok)
	s.Equal(filepath.Join("/home/runner", ".kube", "config"), kubeconfig)
	_, ok = e2eEnvValue(spec.Env, "DOCKER_HOST")
	s.False(ok)
}

func (s *E2ECommandSuite) TestRunWindowsUsesExeWithoutSudo() {
	s.env["DOCKER_HOST"] = "tcp://ignored:2375"

	s.Require().NoError(s.runnerFor(osWindows).run(context.Background(), e2eTargetSuite(true)))

	s.Require().Len(s.fake.specs, 1)
	spec := s.fake.specs[0]
	s.Equal("./e2e.test.exe", spec.Name)
	s.NotEmpty(spec.Env)
	dockerHost, ok := e2eEnvValue(spec.Env, "DOCKER_HOST")
	s.True(ok)
	s.Equal(windowsDockerHost, dockerHost)
	s.Equal(e2eWantTestArgs(e2eTestTimeout, 1), spec.Args)
}

func (s *E2ECommandSuite) TestRunMacOSRunsDirectly() {
	s.Require().NoError(s.runnerFor(osDarwin).run(context.Background(), e2eTargetSuite(true)))

	s.Require().Len(s.fake.specs, 1)
	spec := s.fake.specs[0]
	s.Equal(e2eTestBinPath, spec.Name)
	token, ok := e2eEnvValue(spec.Env, "GH_ACCESS_TOKEN")
	s.True(ok)
	s.Equal(e2eTestToken, token)
	_, ok = e2eEnvValue(spec.Env, "DOCKER_HOST")
	s.False(ok)
}

func (s *E2ECommandSuite) TestRunRootlessPrintsDiagnosticsBeforeRunning() {
	target := e2eTargetSuite(false)
	target.InstallPodman = podmanRootless

	s.Require().NoError(s.runnerFor(osLinux).run(context.Background(), target))

	s.Equal([]string{"id", e2eCmdPodman, e2eTestBinPath}, s.fake.names())
	s.Equal([]string{"info"}, s.fake.specs[1].Args)
	s.Contains(s.out.String(), "HOME=/home/runner\n")
	s.Contains(s.out.String(), "XDG_RUNTIME_DIR=/run/user/1001\n")
}

func (s *E2ECommandSuite) TestRunRootlessStopsWhenPodmanInfoFails() {
	target := e2eTargetSuite(false)
	target.InstallPodman = podmanRootless
	s.fake.errs = map[string]error{e2eCmdPodman: errE2EBoom}

	err := s.runnerFor(osLinux).run(context.Background(), target)

	s.ErrorIs(err, errE2EBoom)
	s.Equal([]string{"id", "podman"}, s.fake.names())
}

func (s *E2ECommandSuite) TestRunFocusedChecksRunFirst() {
	r := s.runnerFor(osLinux)
	r.focused = func(ctx context.Context, runner commandRunner, _ Suite, opts e2eOptions) error {
		s.Equal(s.dir, opts.E2EDir)
		return runner.Run(ctx, commandSpec{Name: "focus-check"})
	}

	s.Require().NoError(r.run(context.Background(), e2eTargetSuite(false)))

	s.Equal([]string{"focus-check", e2eTestBinPath}, s.fake.names())
}

func (s *E2ECommandSuite) TestRunAbortsWhenFocusedChecksFail() {
	r := s.runnerFor(osLinux)
	r.focused = func(context.Context, commandRunner, Suite, e2eOptions) error {
		return errE2EBoom
	}

	err := r.run(context.Background(), e2eTargetSuite(false))

	s.ErrorIs(err, errE2EBoom)
	s.Empty(s.fake.specs)
}

func (s *E2ECommandSuite) TestRunDockerInstalledPostCheckAfterSuccess() {
	target := e2eTargetSuite(false)
	target.PostChecks = []string{postCheckDockerInstalled}

	s.Require().NoError(s.runnerFor(osLinux).run(context.Background(), target))

	s.Equal([]string{e2eTestBinPath, dockerBinary, dockerBinary}, s.fake.names())
	s.Equal([]string{"--version"}, s.fake.specs[1].Args)
	s.Equal([]string{"ps"}, s.fake.specs[2].Args)
}

func (s *E2ECommandSuite) TestRunSkipsPostChecksWhenSuiteFails() {
	target := e2eTargetSuite(false)
	target.PostChecks = []string{postCheckDockerInstalled}
	s.fake.errs = map[string]error{e2eTestBinPath: errE2EBoom}

	err := s.runnerFor(osLinux).run(context.Background(), target)

	s.ErrorIs(err, errE2EBoom)
	s.Equal([]string{e2eTestBinPath}, s.fake.names())
}

func (s *E2ECommandSuite) TestRunRejectsUnknownPostCheck() {
	target := e2eTargetSuite(false)
	target.PostChecks = []string{"no-such-check"}

	err := s.runnerFor(osLinux).run(context.Background(), target)

	s.ErrorIs(err, errUnknownPostCheck)
}

func (s *E2ECommandSuite) TestRunTakesTimeoutAndFlakeAttemptsFromSuite() {
	target := e2eTargetSuite(false)
	target.TestTimeout = "900s"
	target.FlakeAttempts = 3

	s.Require().NoError(s.runnerFor(osLinux).run(context.Background(), target))

	s.Equal(e2eWantTestArgs("900s", 3), s.fake.specs[0].Args)
}

func (s *E2ECommandSuite) TestRunPrintsBanner() {
	s.Require().NoError(s.runnerFor(osLinux).run(context.Background(), e2eTargetSuite(true)))

	s.Contains(s.out.String(), "running suite integration-x on ubuntu-latest (privileged=true)\n")
}

func (s *E2ECommandSuite) TestRunNeverPrintsToken() {
	s.fake.errs = map[string]error{e2eTestBinPath: errE2EBoom}

	err := s.runnerFor(osLinux).run(context.Background(), e2eTargetSuite(false))

	s.Error(err)
	s.NotContains(s.out.String(), e2eTestToken)
	s.NotContains(s.errOut.String(), e2eTestToken)
}

func (s *E2ECommandSuite) TestCLIRunRefusesMicrosandbox() {
	code := e2eCLI([]string{e2eCmdRun, e2eFlagLabel, "up-provider-microsandbox"}, &s.out, &s.errOut)

	s.Equal(2, code)
	s.Contains(s.errOut.String(), "devsy-ci-microsandbox")
}

func (s *E2ECommandSuite) TestCLIRejectsBadUsage() {
	tests := map[string][]string{
		"no subcommand":      {},
		"unknown subcommand": {"bogus"},
		"run without label":  {e2eCmdRun},
		"unknown label":      {e2eCmdRun, e2eFlagLabel, "no-such-suite"},
		"unknown flag":       {e2eCmdRun, "--no-such-flag"},
		"stray argument":     {e2eCmdPrepare, "--bin-src", s.dir, "extra"},
	}
	for name, args := range tests {
		s.Equal(2, e2eCLI(args, &s.out, &s.errOut), name)
	}
}

func (s *E2ECommandSuite) runnerFor(goos string) e2eRunner {
	return e2eRunner{
		runner: s.fake,
		getenv: func(key string) string { return s.env[key] },
		readFile: func(path string) ([]byte, error) {
			s.reads = append(s.reads, path)
			if s.readErr != nil {
				return nil, s.readErr
			}
			return s.report, nil
		},
		focused: e2eNoFocusedChecks,
		opts: e2eOptions{
			E2EDir:  s.dir,
			TempDir: "/tmp/runner-temp",
			GOOS:    goos,
			Stdout:  &s.out,
			Stderr:  &s.errOut,
		},
	}
}

// setReport makes the fake report reader return r as a one-suite Ginkgo JSON report.
func (s *E2ECommandSuite) setReport(r types.Report) {
	data, err := json.Marshal([]types.Report{r})
	s.Require().NoError(err)
	s.report = data
}

func (s *E2ECommandSuite) writeFile(path string, mode os.FileMode) {
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o755))
	s.Require().NoError(os.WriteFile(path, []byte("content"), mode))
	s.Require().NoError(os.Chmod(path, mode))
}

// binSource builds a download tree with devsy binaries in nested directories and some decoys.
func (s *E2ECommandSuite) binSource() string {
	src := filepath.Join(s.T().TempDir(), "devsy-bin")
	s.writeFile(filepath.Join(src, "linux", "devsy-linux-amd64"), 0o700)
	s.writeFile(filepath.Join(src, "mac", "devsy-darwin-arm64"), 0o600)
	s.writeFile(filepath.Join(src, "mac", "notes.txt"), 0o644)
	s.writeFile(filepath.Join(src, "devsy-dir", "inner"), 0o644)
	if runtime.GOOS != osWindows {
		s.Require().
			NoError(os.Symlink(filepath.Join("linux", "devsy-linux-amd64"), filepath.Join(src, "devsy-link")))
	}
	return src
}
