package main

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

// testGroupsYAML is a minimal e2e section with the same group shapes as the
// embedded manifest. Invalid-manifest cases append their suites to it.
const testGroupsYAML = `e2e:
  groups:
    integration:
      runner: ubuntu-latest
      privileged: true
      test_timeout: 1500s
      job_timeout_minutes: 45
      flake_attempts: 1
    microsandbox:
      runner: ubuntu-latest
      privileged: false
      test_timeout: 1200s
      job_timeout_minutes: 25
      flake_attempts: 1
  suites:
`

// manifestCase is one inline manifest that ParseManifest must reject.
type manifestCase struct {
	name string
	yaml string
	want error
}

// manifestSuite covers ParseManifest, LoadManifest and the derived Suite and
// UnitStep helpers.
type manifestSuite struct {
	suite.Suite
	m *Manifest
}

func TestManifestSuite(t *testing.T) {
	suite.Run(t, new(manifestSuite))
}

func (s *manifestSuite) SetupSuite() {
	m, err := LoadManifest()
	s.Require().NoError(err)
	s.m = m
}

// Footgun: the embedded suites.yaml stops parsing, which would fail every PR
// workflow job that reads the matrix.
func (s *manifestSuite) TestEmbeddedManifestLoads() {
	s.NotEmpty(s.m.E2E.Groups)
	s.NotEmpty(s.m.E2E.Suites)
	s.NotEmpty(s.m.Unit.Steps)
	s.NotEmpty(s.m.Unit.Runners)
}

// Footgun: a suite that omits a field silently gets the wrong group default,
// for example an unprivileged suite running with sudo or the wrong timeout.
func (s *manifestSuite) TestSuitesInheritGroupDefaults() {
	ctx := s.mustFind("context", "")
	s.Equal("ubuntu-latest", ctx.Runner)
	s.Equal("1500s", ctx.TestTimeout)
	s.Equal(45, ctx.JobTimeoutMinutes)
	s.Equal(1, ctx.FlakeAttempts)
	s.False(ctx.Resolved)

	micro := s.mustFind("up-provider-microsandbox-images", "")
	s.Equal("1200s", micro.TestTimeout)
	s.Equal(25, micro.JobTimeoutMinutes)
	s.Equal(1, micro.FlakeAttempts)
	s.False(micro.Resolved)
}

// Footgun: an explicit suite override is replaced by the group default, so
// the docker-context job gets the 1500s and 45 minute budget it was tuned away from.
func (s *manifestSuite) TestSuiteOverridesWinOverGroupDefaults() {
	dc := s.mustFind("docker-context", "")
	s.Equal("300s", dc.TestTimeout)
	s.Equal(10, dc.JobTimeoutMinutes)
	s.Equal(1, dc.FlakeAttempts)
	s.True(dc.Resolved, "integration group is privileged and docker-context does not override it")

	tunnel := s.mustFind("tunnel", "")
	s.Equal(2, tunnel.FlakeAttempts)
	s.Equal("1500s", tunnel.TestTimeout, "tunnel inherits the integration timeout")
}

// Footgun: a rootless Podman suite runs privileged, or a rootful one runs
// unprivileged, so the sudo decision in the CI helper is wrong.
func (s *manifestSuite) TestPodmanPrivilegeFollowsMode() {
	rootless := s.mustFind("up-provider-podman-rootless-basic", "")
	s.Equal("rootless", rootless.InstallPodman)
	s.Equal("ubuntu-24.04", rootless.Runner)
	s.Equal("600s", rootless.TestTimeout)
	s.False(rootless.Resolved)

	rootful := s.mustFind("up-provider-podman-rootful-basic", "")
	s.Equal("rootful", rootful.InstallPodman)
	s.True(rootful.Resolved)
}

// Footgun: the Windows provider suite loses its 900s override, or the
// Linux flake_attempts of 2 leaks into the Windows suite that does not set it.
func (s *manifestSuite) TestWindowsSuiteKeepsItsOverride() {
	win := s.mustFind("provider", "windows-latest")
	s.Equal("windows-latest", win.Runner)
	s.Equal("900s", win.TestTimeout)
	s.Equal(20, win.JobTimeoutMinutes)
	s.Equal(1, win.FlakeAttempts)
	s.True(win.Resolved)
}

// Footgun: a microsandbox suite without provider_mode runs with an empty mode,
// and an explicit external mode is overwritten with the parity default.
func (s *manifestSuite) TestMicrosandboxProviderMode() {
	s.Equal("parity", s.mustFind("up-provider-microsandbox-images", "").ProviderMode)
	s.Equal("external", s.mustFind("up-provider-microsandbox-registry-consumer", "").ProviderMode)
	s.Empty(s.mustFind("context", "").ProviderMode, "only microsandbox suites get a provider mode")
}

// Footgun: the GitHub job name drops the podman mode, so rootless and rootful
// jobs of the same label show up as indistinguishable checks.
func (s *manifestSuite) TestSuiteTitle() {
	rootless := s.mustFind("up-provider-podman-rootless-basic", "ubuntu-24.04")
	s.Equal("Test up-provider-podman-rootless-basic (rootless) on ubuntu-24.04", rootless.Title())

	ctx := s.mustFind("context", "")
	s.Equal("Test context on ubuntu-latest", ctx.Title())
}

// Footgun: the runner in the GOOS mapping is wrong, so Windows-only steps run
// on Linux runners or the reverse.
func (s *manifestSuite) TestSuiteOS() {
	tests := []struct {
		runner string
		want   string
	}{
		{runner: "windows-latest", want: osWindows},
		{runner: "macos-latest", want: osDarwin},
		{runner: "ubuntu-latest", want: osLinux},
		{runner: "ubuntu-24.04", want: osLinux},
	}
	for _, tc := range tests {
		s.Equal(tc.want, Suite{Runner: tc.runner}.OS(), "runner %s", tc.runner)
	}
}

// Footgun: an ambiguous label silently resolves to the wrong suite, or a
// runner that is set but matches nothing is accepted.
func (s *manifestSuite) TestFindSuite() {
	_, err := s.m.FindSuite("provider", "")
	s.ErrorIs(err, errInvalidSuite, "provider exists on ubuntu-latest and windows-latest")

	found, err := s.m.FindSuite("provider", "windows-latest")
	s.Require().NoError(err)
	s.Equal("windows-latest", found.Runner)

	_, err = s.m.FindSuite("no-such-label", "")
	s.ErrorIs(err, errInvalidSuite)
}

// Footgun: a docker-wsl suite is parsed without the Windows runner or the spec count that its
// JSON report check depends on, or a plain integration suite is forced to pin a spec count.
func (s *manifestSuite) TestRuntimeDockerWSLSuite() {
	m, err := ParseManifest([]byte(testGroupsYAML +
		"    - { group: integration, label: x, runner: windows-latest, runtime: docker-wsl, expected_spec_count: 2 }\n"))
	s.Require().NoError(err)
	wsl := s.mustFindIn(m, "x", "windows-latest")
	s.Equal(runtimeDockerWSL, wsl.Runtime)
	s.Equal(2, wsl.ExpectedSpecCount)

	_, err = ParseManifest([]byte(testGroupsYAML + "    - { group: integration, label: y }\n"))
	s.NoError(err, "integration suites without a runtime do not need expected_spec_count")
}

// Footgun: a rejected manifest is accepted because a validation rule was lost
// in refactoring, letting a bad suite reach the CI matrix.
func (s *manifestSuite) TestParseRejectsInvalidEntries() {
	for _, tc := range invalidManifestCases() {
		s.Run(tc.name, func() {
			_, err := ParseManifest([]byte(tc.yaml))
			s.ErrorIs(err, tc.want)
		})
	}
}

// Footgun: a misspelled key such as `flake_attempt` is silently ignored and the
// suite runs with the group default instead of the intended value.
func (s *manifestSuite) TestParseRejectsUnknownKey() {
	_, err := ParseManifest(
		[]byte(testGroupsYAML + "    - { group: integration, label: x, flake_attempt: 2 }\n"),
	)
	s.Error(err)
	s.ErrorContains(err, "flake_attempt")
}

// Footgun: RunsOn disagrees with the os list, so a step runs on a platform it
// was meant to skip or is skipped on its own platform.
func (s *manifestSuite) TestUnitStepRunsOn() {
	all := UnitStep{Name: "all", Packages: []string{"./pkg/x"}}
	s.True(all.RunsOn(osWindows), "a step without os runs everywhere")

	posix := UnitStep{Name: "posix", Packages: []string{"./pkg/x"}, OS: []string{osLinux, osDarwin}}
	s.True(posix.RunsOn(osLinux))
	s.True(posix.RunsOn(osDarwin))
	s.False(posix.RunsOn(osWindows))
}

// Footgun: a typo in a fixture name, or a Linux-only fixture on a Windows runner, makes the
// job download an artifact that cannot exist and fail late.
func (s *manifestSuite) TestFixtureValidation() {
	m, err := ParseManifest([]byte(testGroupsYAML +
		"    - { group: integration, label: x, fixtures: [ghfixture-linux] }\n"))
	s.Require().NoError(err)
	s.Equal([]string{fixtureGHFixtureLinux}, s.mustFindIn(m, "x", "ubuntu-latest").Fixtures)

	for _, entry := range []string{
		"    - { group: integration, label: x, fixtures: [nope] }\n",
		"    - { group: integration, label: x, runner: windows-latest, fixtures: [ghfixture-linux] }\n",
	} {
		_, err := ParseManifest([]byte(testGroupsYAML + entry))
		s.ErrorIs(err, errInvalidSuite, entry)
	}
}

func (s *manifestSuite) mustFind(label, runner string) Suite {
	s.T().Helper()
	return s.mustFindIn(s.m, label, runner)
}

func (s *manifestSuite) mustFindIn(m *Manifest, label, runner string) Suite {
	s.T().Helper()
	found, err := m.FindSuite(label, runner)
	s.Require().NoError(err)
	return found
}

func invalidManifestCases() []manifestCase {
	return append(invalidSuiteCases(), invalidUnitStepCases()...)
}

func invalidSuiteCases() []manifestCase {
	return []manifestCase{
		{
			name: "unknown group",
			yaml: testGroupsYAML + "    - { group: nope, label: x }\n",
			want: errUnknownGroup,
		},
		{
			name: "duplicate runner and label",
			yaml: testGroupsYAML +
				"    - { group: integration, label: x }\n" +
				"    - { group: integration, label: x }\n",
			want: errDuplicateSuite,
		},
		{
			name: "microsandbox without expected_spec_count",
			yaml: testGroupsYAML + "    - { group: microsandbox, label: up-provider-microsandbox }\n",
			want: errInvalidSuite,
		},
		{
			name: "bad install_podman value",
			yaml: testGroupsYAML + "    - { group: integration, label: x, install_podman: sometimes }\n",
			want: errInvalidSuite,
		},
		{
			name: "unknown runtime",
			yaml: testGroupsYAML + "    - { group: integration, label: x, runtime: hyperv }\n",
			want: errInvalidSuite,
		},
		{
			name: "docker-wsl on a linux runner",
			yaml: testGroupsYAML +
				"    - { group: integration, label: x, runtime: docker-wsl, expected_spec_count: 1 }\n",
			want: errInvalidSuite,
		},
		{
			name: "docker-wsl without expected_spec_count",
			yaml: testGroupsYAML +
				"    - { group: integration, label: x, runner: windows-latest, runtime: docker-wsl }\n",
			want: errInvalidSuite,
		},
	}
}

func invalidUnitStepCases() []manifestCase {
	return []manifestCase{
		{
			name: "unit step without packages or discover",
			yaml: "unit:\n  steps:\n    - name: bare\n",
			want: errInvalidStep,
		},
		{
			name: "unit step with unknown os",
			yaml: "unit:\n  steps:\n    - name: s\n      packages: [./pkg/x]\n      os: [solaris]\n",
			want: errInvalidStep,
		},
	}
}
