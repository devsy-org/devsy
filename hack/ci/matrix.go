package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
)

const matrixUnitGroup = "unit"

// matrixGroupNames lists the job groups in the order `ci matrix` prints them.
var matrixGroupNames = []string{
	matrixUnitGroup,
	groupUnprivileged,
	groupIntegration,
	groupMicrosandbox,
}

var errUnknownMatrixGroup = errors.New("unknown matrix group")

// matrixDoc is the GitHub Actions matrix object.
type matrixDoc struct {
	Include any `json:"include"`
}

// matrixUnitEntry is one unit-test runner with the GOOS its steps run as.
type matrixUnitEntry struct {
	UnitRunner
	OS string `json:"os"`
}

// matrixEntry is one e2e suite as a job of the workflow matrix.
type matrixEntry struct {
	Name               string `json:"name"`
	Title              string `json:"title"`
	Runner             string `json:"runner"`
	OS                 string `json:"os"`
	Privileged         bool   `json:"privileged"`
	TestTimeout        string `json:"test_timeout"`
	JobTimeoutMinutes  int    `json:"job_timeout_minutes"`
	FlakeAttempts      int    `json:"flake_attempts"`
	FreeDiskSpace      bool   `json:"free_disk_space"`
	InstallKind        bool   `json:"install_kind"`
	PodmanMode         string `json:"podman_mode"`
	RequiresSecret     bool   `json:"requires_secret"`
	ExpectedSpecCount  int    `json:"expected_spec_count"`
	ProviderMode       string `json:"provider_mode"`
	PerformanceCapture bool   `json:"performance_capture"`
	Runtime            string `json:"runtime"`
	SetupDockerWSL     bool   `json:"setup_docker_wsl"`
	SetupKindLinux     bool   `json:"setup_kind_linux"`
	SetupKindWindows   bool   `json:"setup_kind_windows"`
	SetupPodmanLinux   bool   `json:"setup_podman_linux"`
	SetupPodmanWindows bool   `json:"setup_podman_windows"`
	NeedsPodmanHelper  bool   `json:"needs_podman_helper"`
	GHFixtureLinux     bool   `json:"ghfixture_linux"`
}

// matrixCLI prints the job matrices for the PR workflow.
func matrixCLI(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("matrix", flag.ContinueOnError)
	fs.SetOutput(stderr)
	group := fs.String(
		"group",
		"",
		"print only this group: unit, unprivileged, integration or microsandbox",
	)
	secretAvailable := fs.Bool("secret-available", false, "include suites that require a secret")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(
			stderr,
			"ci matrix: unexpected arguments: %s\n",
			strings.Join(fs.Args(), " "),
		)
		return 2
	}
	m, err := LoadManifest()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "ci matrix: %v\n", err)
		return 1
	}
	out, err := matrixOutput(m, *group, *secretAvailable)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "ci matrix: %v\n", err)
		return matrixFailureCode(err)
	}
	if _, err := io.WriteString(stdout, out); err != nil {
		_, _ = fmt.Fprintf(stderr, "ci matrix: write output: %v\n", err)
		return 1
	}
	return 0
}

// matrixFailureCode maps a matrixOutput error to an exit code.
func matrixFailureCode(err error) int {
	if errors.Is(err, errUnknownMatrixGroup) {
		return 2
	}
	return 1
}

// matrixOutput renders the --group JSON line, or without a group one
// `name=<json>` line per group, each terminated by a newline.
func matrixOutput(m *Manifest, group string, secretAvailable bool) (string, error) {
	if group != "" {
		line, err := matrixGroupJSON(m, group, secretAvailable)
		if err != nil {
			return "", err
		}
		return line + "\n", nil
	}
	lines := make([]string, 0, len(matrixGroupNames))
	for _, name := range matrixGroupNames {
		line, err := matrixGroupJSON(m, name, secretAvailable)
		if err != nil {
			return "", err
		}
		lines = append(lines, name+"="+line)
	}
	return strings.Join(lines, "\n") + "\n", nil
}

// matrixGroupJSON returns the compact single-line matrix object of a group.
func matrixGroupJSON(m *Manifest, group string, secretAvailable bool) (string, error) {
	include, err := matrixInclude(m, group, secretAvailable)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(matrixDoc{Include: include}); err != nil {
		return "", fmt.Errorf("encode %s matrix: %w", group, err)
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

// matrixInclude returns the entries of one group. Entry slices are never nil
// so an empty group encodes as [] rather than null.
func matrixInclude(m *Manifest, group string, secretAvailable bool) (any, error) {
	switch group {
	case matrixUnitGroup:
		return unitMatrixEntries(m), nil
	case groupUnprivileged, groupIntegration, groupMicrosandbox:
		return e2eMatrixEntries(m, group, secretAvailable), nil
	default:
		return nil, fmt.Errorf("%w: %q", errUnknownMatrixGroup, group)
	}
}

func unitMatrixEntries(m *Manifest) []matrixUnitEntry {
	entries := make([]matrixUnitEntry, 0, len(m.Unit.Runners))
	for _, r := range m.Unit.Runners {
		entries = append(entries, matrixUnitEntry{UnitRunner: r, OS: Suite{Runner: r.Runner}.OS()})
	}
	return entries
}

func e2eMatrixEntries(m *Manifest, group string, secretAvailable bool) []matrixEntry {
	suites := m.SuitesIn(group)
	entries := make([]matrixEntry, 0, len(suites))
	for _, s := range suites {
		if s.RequiresSecret && !secretAvailable {
			continue
		}
		entries = append(entries, newMatrixEntry(s))
	}
	return entries
}

// newMatrixEntry maps a resolved suite to its matrix job, including the setup
// steps the workflow must run for it.
func newMatrixEntry(s Suite) matrixEntry {
	osName := s.OS()
	linux := osName == osLinux
	windows := osName == osWindows
	hasPodman := s.InstallPodman != ""
	setupPodmanLinux := linux && hasPodman
	setupPodmanWindows := windows && (s.InstallKind || hasPodman)
	return matrixEntry{
		Name:               s.Label,
		Title:              s.Title(),
		Runner:             s.Runner,
		OS:                 osName,
		Privileged:         s.Resolved,
		TestTimeout:        s.TestTimeout,
		JobTimeoutMinutes:  s.JobTimeoutMinutes,
		FlakeAttempts:      s.FlakeAttempts,
		FreeDiskSpace:      s.FreeDiskSpace,
		InstallKind:        s.InstallKind,
		PodmanMode:         s.InstallPodman,
		RequiresSecret:     s.RequiresSecret,
		ExpectedSpecCount:  s.ExpectedSpecCount,
		ProviderMode:       s.ProviderMode,
		PerformanceCapture: s.PerformanceCapture,
		Runtime:            s.Runtime,
		SetupDockerWSL:     s.Runtime == runtimeDockerWSL,
		SetupKindLinux:     linux && s.InstallKind,
		SetupKindWindows:   windows && s.InstallKind,
		SetupPodmanLinux:   setupPodmanLinux,
		SetupPodmanWindows: setupPodmanWindows,
		NeedsPodmanHelper:  setupPodmanLinux || setupPodmanWindows,
		GHFixtureLinux:     linux && slices.Contains(s.Fixtures, fixtureGHFixtureLinux),
	}
}
