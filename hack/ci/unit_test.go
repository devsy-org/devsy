package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

const unitTestManifest = `
unit:
  steps:
    - name: packages
      description: discovered packages
      os: [linux, darwin]
      discover: true
      args: [-race, -short]
    - name: linux-busybox
      description: busybox shell
      os: [linux]
      host_shell: busybox
      packages: [./pkg/inject]
      run: '^TestBootstrap$'
      args: [-race, -count=1]
    - name: windows-transport
      description: windows only
      os: [windows]
      packages: [./pkg/docker]
      args: [-count=1]
    - name: shared
      description: every OS
      packages: [./e2e/tests/up]
      args: [-count=1]
    - name: compose
      description: required test
      os: [linux, darwin]
      packages: [./e2e/tests/up-docker-compose]
      run: '^TestExpectedUIDMapping$'
      require_tests: [TestExpectedUIDMapping]
      args: [-race, -count=1, -v]
`

const (
	unitSharedStep    = "shared"
	unitGoListCommand = "go list -f {{.ImportPath}}|{{.Dir}} ./..."
	unitRequiredTest  = "TestA"
	unitPkgA          = "./a"
	unitProfile       = "p.out"
	unitListFlag      = "--list"
)

var errUnitFake = errors.New("fake failure")

// unitFakeRunner records every command line and answers it through respond.
type unitFakeRunner struct {
	calls   []string
	respond func(line string) (string, error)
}

func (f *unitFakeRunner) Run(_ context.Context, spec commandSpec) error {
	line := strings.Join(append([]string{spec.Name}, spec.Args...), " ")
	f.calls = append(f.calls, line)
	if f.respond == nil {
		return nil
	}
	out, err := f.respond(line)
	if spec.Stdout != nil {
		_, _ = io.WriteString(spec.Stdout, out)
	}
	return err
}

// unitHarness is a unitRunner wired to a fake runner and captured output.
type unitHarness struct {
	fake   *unitFakeRunner
	stdout *bytes.Buffer
	stderr *bytes.Buffer
	runner unitRunner
}

func newUnitHarness(
	root, goos string,
	env map[string]string,
	respond func(string) (string, error),
) *unitHarness {
	h := &unitHarness{
		fake:   &unitFakeRunner{respond: respond},
		stdout: &bytes.Buffer{},
		stderr: &bytes.Buffer{},
	}
	h.runner = unitRunner{
		runner: h.fake,
		goos:   goos,
		root:   root,
		env:    func(key string) string { return env[key] },
		stdout: h.stdout,
		stderr: h.stderr,
	}
	return h
}

// unitFailOn returns a responder that fails every command starting with prefix.
func unitFailOn(prefix string) func(string) (string, error) {
	return func(line string) (string, error) {
		if strings.HasPrefix(line, prefix) {
			return "", errUnitFake
		}
		return "", nil
	}
}

// unitFilterFixture returns a repo root that does not exist on disk and a helper
// that joins slash-separated relative paths onto it.
func unitFilterFixture(root string) func(string) string {
	return func(rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }
}

func unitNames(steps []UnitStep) []string {
	names := make([]string, 0, len(steps))
	for _, step := range steps {
		names = append(names, step.Name)
	}
	return names
}

type unitSuite struct {
	suite.Suite
	root  string
	steps []UnitStep
}

func TestUnitSuite(t *testing.T) {
	suite.Run(t, new(unitSuite))
}

func (s *unitSuite) SetupSuite() {
	m, err := ParseManifest([]byte(unitTestManifest))
	s.Require().NoError(err)
	s.steps = m.Unit.Steps
}

func (s *unitSuite) SetupTest() {
	s.root = filepath.Join(s.T().TempDir(), "devsy")
}

// listing returns a go list responder answering with output and running every other command.
func listing(output string) func(string) (string, error) {
	return func(line string) (string, error) {
		if strings.HasPrefix(line, "go list") {
			return output, nil
		}
		return "", nil
	}
}

func (s *unitSuite) TestSelectUnitStepsByOS() {
	tests := []struct {
		goos string
		want []string
	}{
		{goos: osLinux, want: []string{"packages", "linux-busybox", unitSharedStep, "compose"}},
		{goos: osDarwin, want: []string{"packages", unitSharedStep, "compose"}},
		{goos: osWindows, want: []string{"windows-transport", unitSharedStep}},
	}
	for _, tc := range tests {
		s.Run(tc.goos, func() {
			got, err := selectUnitSteps(s.steps, tc.goos, "")
			s.Require().NoError(err)
			s.Equal(tc.want, unitNames(got))
		})
	}
}

func (s *unitSuite) TestSelectUnitStepsByName() {
	got, err := selectUnitSteps(s.steps, osWindows, unitSharedStep)
	s.Require().NoError(err)
	s.Equal([]string{unitSharedStep}, unitNames(got))

	_, err = selectUnitSteps(s.steps, osDarwin, "linux-busybox")
	s.ErrorIs(err, errNoUnitSteps)
}

func (s *unitSuite) TestDiscoverRunsFilteredPackages() {
	h := s.harness(osLinux, listing(strings.Join([]string{
		"example.com/devsy/pkg/foo|" + s.at("pkg/foo"),
		"example.com/devsy/pkg/test|" + s.at("pkg/test"),
	}, "\n")+"\n"))

	s.Equal(unitSuccessExitCode, h.runner.runAll([]UnitStep{s.step("packages")}))
	s.Equal([]string{
		unitGoListCommand,
		"go test -race -short example.com/devsy/pkg/foo",
	}, h.fake.calls)
}

func (s *unitSuite) TestCoverageOnlyForDiscoverSteps() {
	h := s.harness(osLinux, listing("example.com/devsy/pkg/foo|"+s.at("pkg/foo")+"\n"))
	h.runner.coverprofile = "dist/profile.out"

	s.Equal(
		unitSuccessExitCode,
		h.runner.runAll([]UnitStep{s.step("packages"), s.step(unitSharedStep)}),
	)
	s.Equal([]string{
		unitGoListCommand,
		"go test -race -short -coverprofile=dist/profile.out -covermode=atomic example.com/devsy/pkg/foo",
		"go test -count=1 ./e2e/tests/up",
	}, h.fake.calls)
}

func (s *unitSuite) TestDiscoverFailureFailsStep() {
	h := s.harness(osLinux, unitFailOn("go list"))

	err := h.runner.runStep(s.step("packages"))
	s.ErrorIs(err, errUnitFake)
	s.Equal([]string{unitGoListCommand}, h.fake.calls)
}

func (s *unitSuite) TestRequireTestsRunsWhenListed() {
	h := s.harness(osLinux, func(line string) (string, error) {
		if strings.Contains(line, "-list") {
			return "TestExpectedUIDMapping\nok  \texample.com/x\t0.1s\n", nil
		}
		return "", nil
	})

	s.Equal(unitSuccessExitCode, h.runner.runAll([]UnitStep{s.step("compose")}))
	s.Equal([]string{
		"go test ./e2e/tests/up-docker-compose -list ^TestExpectedUIDMapping$",
		"go test -race -count=1 -v -run ^TestExpectedUIDMapping$ ./e2e/tests/up-docker-compose",
	}, h.fake.calls)
}

func (s *unitSuite) TestRequireTestsFailsWhenMissing() {
	h := s.harness(osLinux, func(line string) (string, error) {
		if strings.Contains(line, "-list") {
			return "TestExpectedUIDMappingRenamed\n", nil
		}
		return "", nil
	})

	err := h.runner.runStep(s.step("compose"))
	s.ErrorIs(err, errRequiredTestMissing)
	s.Contains(err.Error(), "TestExpectedUIDMapping")
	s.Equal([]string{
		"go test ./e2e/tests/up-docker-compose -list ^TestExpectedUIDMapping$",
	}, h.fake.calls)
}

func (s *unitSuite) TestRequireTestsPatternWithoutRun() {
	step := UnitStep{RequireTests: []string{unitRequiredTest, "TestB"}}
	s.Equal("^(TestA|TestB)$", unitTestPattern(step))
	s.Equal(
		"^TestC$",
		unitTestPattern(UnitStep{Run: "^TestC$", RequireTests: []string{unitRequiredTest}}),
	)
}

func (s *unitSuite) TestMissingTests() {
	tests := []struct {
		name    string
		listing string
		want    []string
	}{
		{name: "exact lines", listing: "TestA\nTestB\n", want: nil},
		{name: "crlf lines", listing: "TestA\r\nTestB\r\n", want: nil},
		{name: "prefix is not a match", listing: "TestAlpha\n", want: []string{unitRequiredTest}},
		{name: "empty listing", listing: "", want: []string{unitRequiredTest}},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			got := missingTests([]string{unitRequiredTest}, tc.listing)
			s.Equal(tc.want, got)
		})
	}
}

func (s *unitSuite) TestBusyboxRestoresShellWhenTestFails() {
	h := s.harness(osLinux, func(line string) (string, error) {
		if strings.HasPrefix(line, "readlink") {
			return "dash\n", nil
		}
		if strings.HasPrefix(line, "go test") {
			return "", errUnitFake
		}
		return "", nil
	})

	s.Equal(unitFailureExitCode, h.runner.runAll([]UnitStep{s.step("linux-busybox")}))
	s.Equal([]string{
		"sudo apt-get update -qq",
		"sudo apt-get install -y busybox-static",
		"readlink /bin/sh",
		"sudo ln -sf /bin/busybox /bin/sh",
		"go test -race -count=1 -run ^TestBootstrap$ ./pkg/inject",
		"sudo ln -sf dash /bin/sh",
	}, h.fake.calls)
	s.Contains(h.stderr.String(), "FAILED: linux-busybox: ")
}

func (s *unitSuite) TestBusyboxRestoreFailureIsReported() {
	h := s.harness(osLinux, func(line string) (string, error) {
		if strings.HasPrefix(line, "readlink") {
			return "dash\n", nil
		}
		if strings.HasPrefix(line, "sudo ln -sf dash") {
			return "", errUnitFake
		}
		return "", nil
	})

	err := h.runner.runStep(s.step("linux-busybox"))
	s.ErrorIs(err, errUnitFake)
}

func (s *unitSuite) TestBusyboxFailsWhenShellIsNotSymlink() {
	h := s.harness(osLinux, func(line string) (string, error) {
		if strings.HasPrefix(line, "readlink") {
			return "\n", nil
		}
		return "", nil
	})

	err := h.runner.runStep(s.step("linux-busybox"))
	s.ErrorIs(err, errShellNotSymlink)
	s.NotContains(h.fake.calls, "sudo ln -sf /bin/busybox /bin/sh")
}

func (s *unitSuite) TestBusyboxRejectedOffLinux() {
	h := s.harness(osDarwin, nil)

	err := h.runner.runStep(s.step("linux-busybox"))
	s.ErrorIs(err, errHostShellNeedsLinux)
	s.Empty(h.fake.calls)
}

func (s *unitSuite) TestUnknownHostShellRejected() {
	h := s.harness(osLinux, nil)

	err := h.runner.runStep(UnitStep{Name: "odd", Packages: []string{unitPkgA}, HostShell: "zsh"})
	s.ErrorIs(err, errUnknownHostShell)
	s.Empty(h.fake.calls)
}

func (s *unitSuite) TestRunAllContinuesAfterFailure() {
	h := s.harness(osLinux, unitFailOn("go test ./a"))
	steps := []UnitStep{
		{Name: "first", Packages: []string{unitPkgA}},
		{Name: "second", Packages: []string{"./b"}},
	}

	s.Equal(unitFailureExitCode, h.runner.runAll(steps))
	s.Equal([]string{"go test ./a", "go test ./b"}, h.fake.calls)
	s.Contains(h.stderr.String(), "FAILED: first: ")
	s.NotContains(h.stderr.String(), "second")
}

func (s *unitSuite) TestRunAllSucceeds() {
	h := s.harness(osLinux, nil)

	s.Equal(unitSuccessExitCode, h.runner.runAll([]UnitStep{s.step(unitSharedStep)}))
	s.Empty(h.stderr.String())
}

func (s *unitSuite) TestPlainHeaderOutsideActions() {
	h := newUnitHarness(s.root, osLinux, nil, nil)

	s.Equal(unitSuccessExitCode, h.runner.runAll([]UnitStep{s.step(unitSharedStep)}))
	s.Equal("=== shared: every OS\n", h.stdout.String())
}

func (s *unitSuite) TestGitHubActionsGroups() {
	h := newUnitHarness(s.root, osLinux, map[string]string{gitHubActionsEnv: "true"}, nil)

	s.Equal(unitSuccessExitCode, h.runner.runAll([]UnitStep{s.step(unitSharedStep)}))
	s.Equal("::group::shared\n::endgroup::\n", h.stdout.String())
}

func (s *unitSuite) TestListPrintsWithoutRunning() {
	h := s.harness(osLinux, nil)
	steps, err := selectUnitSteps(s.steps, osLinux, "")
	s.Require().NoError(err)

	s.Equal(unitSuccessExitCode, h.runner.runSelected(steps, true))
	s.Equal("packages\nlinux-busybox\nshared\ncompose\n", h.stdout.String())
	s.Empty(h.fake.calls)
}

func (s *unitSuite) TestGoTestArgs() {
	h := s.harness(osLinux, nil)
	h.runner.coverprofile = unitProfile

	s.Equal([]string{"test", "-count=1", "-run", "^X$", unitPkgA},
		h.runner.goTestArgs(UnitStep{Args: []string{"-count=1"}, Run: "^X$"}, []string{unitPkgA}))
	s.Equal([]string{"test", "-race", "-coverprofile=p.out", "-covermode=atomic", unitPkgA},
		h.runner.goTestArgs(UnitStep{Args: []string{"-race"}, Discover: true}, []string{unitPkgA}))
}

func (s *unitSuite) TestParseUnitFlags() {
	var stderr bytes.Buffer
	f, err := parseUnitFlags([]string{
		"--os", osWindows, "--step", unitSharedStep, "--coverprofile", unitProfile, unitListFlag,
	}, &stderr)
	s.Require().NoError(err)
	s.Equal(
		unitFlags{goos: osWindows, step: unitSharedStep, coverprofile: unitProfile, list: true},
		f,
	)

	_, err = parseUnitFlags([]string{"extra"}, &stderr)
	s.ErrorIs(err, errUnexpectedUnitArgs)

	_, err = parseUnitFlags([]string{"--bogus"}, &stderr)
	s.Error(err)
}

func (s *unitSuite) TestUnitCLIListsRealManifest() {
	var stdout, stderr bytes.Buffer

	s.Equal(
		unitSuccessExitCode,
		unitCLI([]string{unitListFlag, "--os", osWindows}, &stdout, &stderr),
	)
	s.Contains(stdout.String(), "windows-transport\n")
	s.NotContains(stdout.String(), "linux-busybox")

	s.Equal(
		unitFailureExitCode,
		unitCLI([]string{unitListFlag, "--step", "no-such-step"}, &stdout, &stderr),
	)
	s.Equal(unitUsageExitCode, unitCLI([]string{"--bogus"}, &stdout, &stderr))
}

func (s *unitSuite) TestFilterUnitTestPackagesKeepsPackages() {
	root := s.root
	at := unitFilterFixture(root)

	tests := []struct {
		name   string
		goList string
		want   []string
	}{
		{
			name:   "keeps package under root",
			goList: "example.com/devsy/pkg/foo|" + at("pkg/foo") + "\n",
			want:   []string{"example.com/devsy/pkg/foo"},
		},
		{
			name:   "keeps root package",
			goList: "example.com/devsy|" + root + "\n",
			want:   []string{"example.com/devsy"},
		},
		{
			name: "excludes test and e2e directories",
			goList: strings.Join([]string{
				"example.com/devsy/test/helpers|" + at("test/helpers"),
				"example.com/devsy/pkg/test|" + at("pkg/test"),
				"example.com/devsy/e2e|" + at("e2e"),
				"example.com/devsy/e2e/framework|" + at("e2e/framework"),
				"example.com/devsy/pkg/e2e/x|" + at("pkg/e2e/x"),
				"example.com/devsy/pkg/foo|" + at("pkg/foo"),
			}, "\n") + "\n",
			want: []string{"example.com/devsy/pkg/foo"},
		},
		{
			name: "keeps names that only contain test or e2e",
			goList: strings.Join([]string{
				"example.com/devsy/pkg/testing|" + at("pkg/testing"),
				"example.com/devsy/pkg/tests|" + at("pkg/tests"),
				"example.com/devsy/pkg/e2equal|" + at("pkg/e2equal"),
			}, "\n") + "\n",
			want: []string{
				"example.com/devsy/pkg/testing",
				"example.com/devsy/pkg/tests",
				"example.com/devsy/pkg/e2equal",
			},
		},
	}

	for _, tc := range tests {
		s.Run(tc.name, func() {
			got, err := filterUnitTestPackages(tc.goList, root)
			s.Require().NoError(err)
			s.Equal(tc.want, got)
		})
	}
}

func (s *unitSuite) TestFilterUnitTestPackagesRejects() {
	root := s.root
	at := unitFilterFixture(root)
	sibling := root + "2"

	tests := []struct {
		name    string
		goList  string
		wantErr error
	}{
		{
			name:    "rejects sibling sharing root prefix",
			goList:  "example.com/x|" + filepath.Join(sibling, "x") + "\n",
			wantErr: errOutsideRepository,
		},
		{
			name: "rejects directory outside root",
			goList: "example.com/elsewhere|" + filepath.Join(
				filepath.Dir(root),
				"elsewhere",
			) + "\n",
			wantErr: errOutsideRepository,
		},
		{
			name:    "rejects listing where every package is excluded",
			goList:  "example.com/devsy/e2e|" + at("e2e") + "\n",
			wantErr: errNoUnitTestPackages,
		},
		{
			name:    "rejects empty listing",
			goList:  "",
			wantErr: errNoUnitTestPackages,
		},
		{
			name:    "rejects line without separator",
			goList:  "example.com/devsy/pkg/foo\n",
			wantErr: errMalformedGoList,
		},
	}

	for _, tc := range tests {
		s.Run(tc.name, func() {
			_, err := filterUnitTestPackages(tc.goList, root)
			s.ErrorIs(err, tc.wantErr)
		})
	}
}

func (s *unitSuite) TestIsUnderRoot() {
	s.True(isUnderRoot("/repo", "/repo"))
	s.True(isUnderRoot("/repo/pkg", "/repo"))
	s.False(isUnderRoot("/repo2/pkg", "/repo"))
}

func (s *unitSuite) at(rel string) string {
	return unitFilterFixture(s.root)(rel)
}

func (s *unitSuite) step(name string) UnitStep {
	for _, step := range s.steps {
		if step.Name == name {
			return step
		}
	}
	s.FailNow("no step named " + name)
	return UnitStep{}
}

func (s *unitSuite) harness(goos string, respond func(string) (string, error)) *unitHarness {
	return newUnitHarness(s.root, goos, nil, respond)
}
