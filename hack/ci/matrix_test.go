package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

// matrixTestManifest is a small manifest with one suite per interesting case.
const matrixTestManifest = `
unit:
  runners:
    - { runner: ubuntu-latest, timeout_minutes: 30 }
    - { runner: windows-latest, timeout_minutes: 30 }
e2e:
  groups:
    unprivileged:
      runner: ubuntu-latest
      privileged: false
      test_timeout: 1500s
      job_timeout_minutes: 45
      flake_attempts: 1
    integration:
      runner: ubuntu-latest
      privileged: true
      test_timeout: 1500s
      job_timeout_minutes: 45
      flake_attempts: 1
    microsandbox:
      runner: ubuntu-latest
      test_timeout: 1200s
      job_timeout_minutes: 25
      flake_attempts: 1
  suites:
    - { group: unprivileged, label: context }
    - { group: integration, label: ide }
    - { group: integration, label: build, requires_secret: true }
    - { group: microsandbox, label: up-provider-microsandbox, expected_spec_count: 2, performance_capture: true }
    - { group: integration, label: "a<b&c" }
`

const (
	matrixProviderLabel  = "provider"
	matrixUbuntuRunner   = "ubuntu-latest"
	matrixPodmanRunner   = "ubuntu-24.04"
	matrixWindowsRunner  = "windows-latest"
	matrixWindowsTimeout = "900s"
)

type matrixSuite struct {
	suite.Suite
}

func TestMatrixSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(matrixSuite))
}

func (s *matrixSuite) TestLinuxKindSuiteMapping() {
	entry := newMatrixEntry(Suite{
		Group:             groupIntegration,
		Label:             matrixProviderLabel,
		Runner:            matrixUbuntuRunner,
		Resolved:          true,
		TestTimeout:       "1500s",
		JobTimeoutMinutes: 45,
		FlakeAttempts:     2,
		FreeDiskSpace:     true,
		InstallKind:       true,
	})

	s.Equal(matrixEntry{
		Name:              matrixProviderLabel,
		Title:             "Test provider on ubuntu-latest",
		Runner:            matrixUbuntuRunner,
		OS:                osLinux,
		Privileged:        true,
		TestTimeout:       "1500s",
		JobTimeoutMinutes: 45,
		FlakeAttempts:     2,
		FreeDiskSpace:     true,
		InstallKind:       true,
		SetupKindLinux:    true,
	}, entry)
}

func (s *matrixSuite) TestRootlessPodmanSuiteMapping() {
	entry := newMatrixEntry(Suite{
		Group:             groupIntegration,
		Label:             matrixProviderLabel,
		Runner:            matrixPodmanRunner,
		InstallPodman:     podmanRootless,
		TestTimeout:       "600s",
		JobTimeoutMinutes: 20,
		FlakeAttempts:     1,
	})

	s.Equal(matrixEntry{
		Name:              matrixProviderLabel,
		Title:             "Test provider (rootless) on ubuntu-24.04",
		Runner:            matrixPodmanRunner,
		OS:                osLinux,
		TestTimeout:       "600s",
		JobTimeoutMinutes: 20,
		FlakeAttempts:     1,
		PodmanMode:        podmanRootless,
		SetupPodmanLinux:  true,
		NeedsPodmanHelper: true,
	}, entry)
}

func (s *matrixSuite) TestWindowsKindSuiteMapping() {
	entry := newMatrixEntry(Suite{
		Group:             groupIntegration,
		Label:             matrixProviderLabel,
		Runner:            matrixWindowsRunner,
		Resolved:          true,
		TestTimeout:       matrixWindowsTimeout,
		JobTimeoutMinutes: 20,
		FlakeAttempts:     1,
		InstallKind:       true,
	})

	// Windows runs Podman for any kind suite too (Docker API served by Podman).
	s.Equal(matrixEntry{
		Name:               matrixProviderLabel,
		Title:              "Test provider on windows-latest",
		Runner:             matrixWindowsRunner,
		OS:                 osWindows,
		Privileged:         true,
		TestTimeout:        matrixWindowsTimeout,
		JobTimeoutMinutes:  20,
		FlakeAttempts:      1,
		InstallKind:        true,
		SetupKindWindows:   true,
		SetupPodmanWindows: true,
		NeedsPodmanHelper:  true,
	}, entry)
}

func (s *matrixSuite) TestWindowsPodmanOnlySuiteMapping() {
	entry := newMatrixEntry(Suite{
		Group:             groupIntegration,
		Label:             "task-cancel",
		Runner:            matrixWindowsRunner,
		InstallPodman:     podmanRootful,
		Resolved:          true,
		TestTimeout:       matrixWindowsTimeout,
		JobTimeoutMinutes: 20,
		FlakeAttempts:     1,
	})

	s.Equal(matrixEntry{
		Name:               "task-cancel",
		Title:              "Test task-cancel (rootful) on windows-latest",
		Runner:             matrixWindowsRunner,
		OS:                 osWindows,
		Privileged:         true,
		TestTimeout:        matrixWindowsTimeout,
		JobTimeoutMinutes:  20,
		FlakeAttempts:      1,
		PodmanMode:         podmanRootful,
		SetupPodmanWindows: true,
		NeedsPodmanHelper:  true,
	}, entry)
}

func (s *matrixSuite) TestDockerWSLSuiteMapping() {
	entry := newMatrixEntry(Suite{
		Group:             groupIntegration,
		Label:             "docker-wsl-provider",
		Runner:            matrixWindowsRunner,
		Resolved:          true,
		TestTimeout:       matrixWindowsTimeout,
		JobTimeoutMinutes: 20,
		FlakeAttempts:     1,
		ExpectedSpecCount: 3,
		Runtime:           runtimeDockerWSL,
	})

	s.Equal(matrixEntry{
		Name:              "docker-wsl-provider",
		Title:             "Test docker-wsl-provider on windows-latest",
		Runner:            matrixWindowsRunner,
		OS:                osWindows,
		Privileged:        true,
		TestTimeout:       matrixWindowsTimeout,
		JobTimeoutMinutes: 20,
		FlakeAttempts:     1,
		ExpectedSpecCount: 3,
		Runtime:           runtimeDockerWSL,
		SetupDockerWSL:    true,
	}, entry)

	line, err := json.Marshal(entry)
	s.Require().NoError(err)
	s.Contains(
		string(line),
		`"performance_capture":false,"runtime":"docker-wsl","setup_docker_wsl":true,"setup_kind_linux":false`,
	)
}

func (s *matrixSuite) TestPlainSuiteHasNoRuntime() {
	entry := newMatrixEntry(Suite{
		Group:  groupIntegration,
		Label:  matrixProviderLabel,
		Runner: matrixUbuntuRunner,
	})

	s.Empty(entry.Runtime)
	s.False(entry.SetupDockerWSL)
}

func (s *matrixSuite) TestSecretSuitesOmittedUnlessAvailable() {
	m := s.testManifest()

	withoutSecret, err := matrixOutput(m, groupIntegration, false)
	s.Require().NoError(err)
	withSecret, err := matrixOutput(m, groupIntegration, true)
	s.Require().NoError(err)

	names := func(line string) []string {
		var out []string
		for _, e := range matrixIncludeOf[matrixEntry](s, line) {
			out = append(out, e.Name)
		}
		return out
	}
	s.Equal([]string{"ide", "a<b&c"}, names(withoutSecret))
	s.Equal([]string{"ide", "build", "a<b&c"}, names(withSecret))
}

func (s *matrixSuite) TestGroupFlagPrintsOnlyThatGroup() {
	code, stdout, stderr := s.runCLI("--group", groupUnprivileged)

	s.Equal(0, code, stderr)
	s.Empty(stderr)
	s.Equal(1, strings.Count(stdout, "\n"))
	s.True(strings.HasPrefix(stdout, `{"include":[`), stdout)
	s.NotContains(stdout, "=")
	m, err := LoadManifest()
	s.Require().NoError(err)
	entries := matrixIncludeOf[matrixEntry](s, stdout)
	s.Require().Len(entries, len(m.SuitesIn(groupUnprivileged)))
	s.Equal("context", entries[0].Name)
}

func (s *matrixSuite) TestNoGroupPrintsFourLinesInOrder() {
	code, stdout, stderr := s.runCLI()

	s.Equal(0, code, stderr)
	s.Empty(stderr)
	lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	s.Require().Len(lines, 4)
	for i, name := range []string{"unit", groupUnprivileged, groupIntegration, groupMicrosandbox} {
		s.True(strings.HasPrefix(lines[i], name+`={"include":[`), lines[i])
		s.True(json.Valid([]byte(strings.TrimPrefix(lines[i], name+"="))), lines[i])
	}
}

func (s *matrixSuite) TestUnitEntriesCarryOS() {
	out, err := matrixOutput(s.testManifest(), matrixUnitGroup, false)
	s.Require().NoError(err)

	s.Equal(`{"include":[`+
		`{"runner":"ubuntu-latest","timeout_minutes":30,"os":"linux"},`+
		`{"runner":"windows-latest","timeout_minutes":30,"os":"windows"}]}`+"\n", out)
}

func (s *matrixSuite) TestCompactJSONDoesNotEscapeHTML() {
	m := s.testManifest()
	line, err := matrixGroupJSON(m, groupIntegration, true)
	s.Require().NoError(err)

	s.Contains(line, `"name":"a<b&c"`)
	s.NotContains(line, "\\u003c")
	s.NotContains(line, "\\u0026")
	s.NotContains(line, "\n")
}

func (s *matrixSuite) TestEmptyGroupEncodesAsArray() {
	line, err := matrixGroupJSON(&Manifest{}, groupMicrosandbox, false)

	s.Require().NoError(err)
	s.Equal(`{"include":[]}`, line)
}

func (s *matrixSuite) TestUnknownGroupExitsWithUsageCode() {
	code, stdout, stderr := s.runCLI("--group", "bogus")

	s.Equal(2, code)
	s.Empty(stdout)
	s.Contains(stderr, errUnknownMatrixGroup.Error())
	s.Contains(stderr, `"bogus"`)
}

func (s *matrixSuite) TestUnexpectedArgumentsExitWithUsageCode() {
	code, stdout, stderr := s.runCLI("extra")

	s.Equal(2, code)
	s.Empty(stdout)
	s.Contains(stderr, "unexpected arguments: extra")
}

func (s *matrixSuite) TestEmbeddedManifestEntriesAreComplete() {
	m, err := LoadManifest()
	s.Require().NoError(err)

	seen := map[string]bool{}
	for _, group := range []string{groupUnprivileged, groupIntegration, groupMicrosandbox} {
		for _, suiteDef := range m.SuitesIn(group) {
			entry := newMatrixEntry(suiteDef)
			msg := fmt.Sprintf("%s/%s", group, suiteDef.Label)
			s.NotEmpty(entry.Name, msg)
			s.NotEmpty(entry.Title, msg)
			s.NotEmpty(entry.Runner, msg)
			s.NotEmpty(entry.TestTimeout, msg)
			s.Positive(entry.JobTimeoutMinutes, msg)

			key := entry.Runner + "/" + entry.Name
			s.False(seen[key], "duplicate runner/label %s", key)
			seen[key] = true
		}
	}
	s.NotEmpty(seen)
}

func (s *matrixSuite) testManifest() *Manifest {
	s.T().Helper()
	m, err := ParseManifest([]byte(matrixTestManifest))
	s.Require().NoError(err)
	return m
}

func (s *matrixSuite) runCLI(args ...string) (int, string, string) {
	s.T().Helper()
	var stdout, stderr bytes.Buffer
	code := matrixCLI(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// matrixIncludeOf decodes the include array of one matrix line.
func matrixIncludeOf[T any](s *matrixSuite, line string) []T {
	s.T().Helper()
	var doc struct {
		Include []T `json:"include"`
	}
	s.Require().NoError(json.Unmarshal([]byte(line), &doc))
	return doc.Include
}
