package main

import (
	_ "embed"
	"errors"
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed suites.yaml
var embeddedSuites []byte

const (
	groupUnprivileged = "unprivileged"
	groupIntegration  = "integration"
	groupMicrosandbox = "microsandbox"

	osLinux   = "linux"
	osWindows = "windows"
	osDarwin  = "darwin"

	podmanRootless = "rootless"
	podmanRootful  = "rootful"

	defaultProviderMode = "parity"

	fixtureGHFixtureLinux = "ghfixture-linux"

	// runtimeDockerWSL is a Windows suite whose Docker daemon is a WSL2 fixture the workflow provisions.
	runtimeDockerWSL = "docker-wsl"
)

var (
	errUnknownGroup   = errors.New("unknown group")
	errDuplicateSuite = errors.New("duplicate suite")
	errInvalidSuite   = errors.New("invalid suite")
	errInvalidStep    = errors.New("invalid unit step")
)

// Manifest is the parsed hack/ci/suites.yaml.
type Manifest struct {
	Unit UnitTier `yaml:"unit"`
	E2E  E2ETier  `yaml:"e2e"`
}

// UnitTier describes how Go unit tests run.
type UnitTier struct {
	Runners  []UnitRunner `yaml:"runners"`
	Steps    []UnitStep   `yaml:"steps"`
	Excluded []Exclusion  `yaml:"excluded"`
}

// UnitRunner is one runner of the unit-test job matrix.
type UnitRunner struct {
	Runner         string `yaml:"runner"          json:"runner"`
	TimeoutMinutes int    `yaml:"timeout_minutes" json:"timeout_minutes"`
}

// UnitStep is one `go test` invocation. Steps run in manifest order.
type UnitStep struct {
	Name         string   `yaml:"name"`
	Description  string   `yaml:"description"`
	OS           []string `yaml:"os"`
	Discover     bool     `yaml:"discover"`
	HostShell    string   `yaml:"host_shell"`
	Packages     []string `yaml:"packages"`
	Run          string   `yaml:"run"`
	RequireTests []string `yaml:"require_tests"`
	Args         []string `yaml:"args"`
}

// Exclusion records a package with tests that is deliberately not a unit test.
type Exclusion struct {
	Package string `yaml:"package"`
	Reason  string `yaml:"reason"`
}

// E2ETier describes the Ginkgo suites.
type E2ETier struct {
	Groups  map[string]Group `yaml:"groups"`
	Ungated []Ungated        `yaml:"ungated"`
	Suites  []Suite          `yaml:"suites"`
}

// Group holds the defaults inherited by its suites.
type Group struct {
	Description       string `yaml:"description"`
	Runner            string `yaml:"runner"`
	Privileged        bool   `yaml:"privileged"`
	TestTimeout       string `yaml:"test_timeout"`
	JobTimeoutMinutes int    `yaml:"job_timeout_minutes"`
	FlakeAttempts     int    `yaml:"flake_attempts"`
}

// Ungated is a Ginkgo label that PR CI intentionally does not select directly.
type Ungated struct {
	Label  string `yaml:"label"`
	Reason string `yaml:"reason"`
}

// FocusedCheck is a single-spec run that must pass exactly once before the
// suite's main run, optionally under a required numeric identity.
type FocusedCheck struct {
	Name       string `yaml:"name"`
	Focus      string `yaml:"focus"`
	ExpectLeaf string `yaml:"expect_leaf"`
	RequireUID int    `yaml:"require_uid"`
	RequireGID int    `yaml:"require_gid"`
}

// Suite is one Ginkgo label executed as one CI job. After Resolve, every
// inherited field is populated.
type Suite struct {
	Group  string `yaml:"group"`
	Label  string `yaml:"label"`
	Runner string `yaml:"runner"`
	// Privileged selects sudo for the e2e binary on Linux. Nil inherits the group.
	Privileged        *bool          `yaml:"privileged"`
	TestTimeout       string         `yaml:"test_timeout"`
	JobTimeoutMinutes int            `yaml:"job_timeout_minutes"`
	FlakeAttempts     int            `yaml:"flake_attempts"`
	FreeDiskSpace     bool           `yaml:"free_disk_space"`
	InstallKind       bool           `yaml:"install_kind"`
	InstallPodman     string         `yaml:"install_podman"`
	RequiresSecret    bool           `yaml:"requires_secret"`
	HostSetup         []string       `yaml:"host_setup"`
	PostChecks        []string       `yaml:"post_checks"`
	FocusedChecks     []FocusedCheck `yaml:"focused_checks"`

	ExpectedSpecCount  int    `yaml:"expected_spec_count"`
	ProviderMode       string `yaml:"provider_mode"`
	PerformanceCapture bool   `yaml:"performance_capture"`
	Runtime            string `yaml:"runtime"`
	// Fixtures names prebuilt artifacts the job downloads next to the e2e binary.
	Fixtures []string `yaml:"fixtures"`

	// Resolved is the effective privilege after group inheritance.
	Resolved bool `yaml:"-"`
}

// LoadManifest parses and validates the embedded manifest.
func LoadManifest() (*Manifest, error) {
	return ParseManifest(embeddedSuites)
}

// ParseManifest parses, resolves and validates manifest bytes.
func ParseManifest(data []byte) (*Manifest, error) {
	var m Manifest
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("parse suites manifest: %w", err)
	}
	if err := m.resolve(); err != nil {
		return nil, err
	}
	if err := m.validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

func validPodmanMode(mode string) bool {
	return mode == "" || mode == podmanRootless || mode == podmanRootful
}

// RunsOn reports whether the step applies to a GOOS value.
func (u UnitStep) RunsOn(goos string) bool {
	return len(u.OS) == 0 || slices.Contains(u.OS, goos)
}

func (u UnitStep) validate() error {
	if u.Name == "" || (!u.Discover && len(u.Packages) == 0) {
		return fmt.Errorf("%w: %q needs a name and packages or discover", errInvalidStep, u.Name)
	}
	for _, os := range u.OS {
		if os != osLinux && os != osDarwin && os != osWindows {
			return fmt.Errorf("%w: %q has os %q", errInvalidStep, u.Name, os)
		}
	}
	return nil
}

// OS returns the GOOS value implied by the suite's runner label.
func (s Suite) OS() string {
	switch {
	case strings.HasPrefix(s.Runner, "windows"):
		return osWindows
	case strings.HasPrefix(s.Runner, "macos"):
		return osDarwin
	default:
		return osLinux
	}
}

// Title is the GitHub job display name.
func (s Suite) Title() string {
	mode := ""
	if s.InstallPodman != "" {
		mode = " (" + s.InstallPodman + ")"
	}
	return fmt.Sprintf("Test %s%s on %s", s.Label, mode, s.Runner)
}

func (s Suite) validate() error {
	switch {
	case s.Label == "":
		return fmt.Errorf("%w: empty label", errInvalidSuite)
	case !s.hasRequiredFields():
		return fmt.Errorf(
			"%w: %s is missing runner, timeouts or flake attempts",
			errInvalidSuite,
			s.Label,
		)
	case !validPodmanMode(s.InstallPodman):
		return fmt.Errorf("%w: %s has install_podman %q", errInvalidSuite, s.Label, s.InstallPodman)
	case s.requiresSpecCount() && s.ExpectedSpecCount <= 0:
		return fmt.Errorf("%w: %s needs expected_spec_count", errInvalidSuite, s.Label)
	}
	if err := s.validateFixtures(); err != nil {
		return err
	}
	return s.validateRuntime()
}

func (s Suite) hasRequiredFields() bool {
	return s.Runner != "" && s.TestTimeout != "" && s.JobTimeoutMinutes > 0 && s.FlakeAttempts > 0
}

// requiresSpecCount reports whether the suite must pin how many specs it selects.
func (s Suite) requiresSpecCount() bool {
	return s.Group == groupMicrosandbox || s.Runtime == runtimeDockerWSL
}

func (s Suite) validateRuntime() error {
	switch {
	case s.Runtime == "":
		return nil
	case s.Runtime != runtimeDockerWSL:
		return fmt.Errorf("%w: %s has runtime %q", errInvalidSuite, s.Label, s.Runtime)
	case s.OS() != osWindows:
		return fmt.Errorf(
			"%w: %s runtime %s needs a windows runner, not %s",
			errInvalidSuite,
			s.Label,
			s.Runtime,
			s.Runner,
		)
	}
	return nil
}

// SuitesIn returns the resolved suites of one group in manifest order.
func (m *Manifest) SuitesIn(group string) []Suite {
	var out []Suite
	for _, s := range m.E2E.Suites {
		if s.Group == group {
			out = append(out, s)
		}
	}
	return out
}

// FindSuite returns the suite with a label on a runner. An empty runner
// matches only when the label is unique.
func (m *Manifest) FindSuite(label, runner string) (Suite, error) {
	var found []Suite
	for _, s := range m.E2E.Suites {
		if s.Label == label && (runner == "" || s.Runner == runner) {
			found = append(found, s)
		}
	}
	if len(found) != 1 {
		return Suite{}, fmt.Errorf(
			"%w: %d suites match label %q runner %q",
			errInvalidSuite,
			len(found),
			label,
			runner,
		)
	}
	return found[0], nil
}

func (m *Manifest) resolve() error {
	for i := range m.E2E.Suites {
		s := &m.E2E.Suites[i]
		g, ok := m.E2E.Groups[s.Group]
		if !ok {
			return fmt.Errorf("%w %q for label %q", errUnknownGroup, s.Group, s.Label)
		}
		s.Runner = firstNonEmpty(s.Runner, g.Runner)
		s.TestTimeout = firstNonEmpty(s.TestTimeout, g.TestTimeout)
		if s.JobTimeoutMinutes == 0 {
			s.JobTimeoutMinutes = g.JobTimeoutMinutes
		}
		if s.FlakeAttempts == 0 {
			s.FlakeAttempts = g.FlakeAttempts
		}
		s.Resolved = g.Privileged
		if s.Privileged != nil {
			s.Resolved = *s.Privileged
		}
		if s.Group == groupMicrosandbox {
			s.ProviderMode = firstNonEmpty(s.ProviderMode, defaultProviderMode)
		}
	}
	return nil
}

func (m *Manifest) validate() error {
	seen := map[string]bool{}
	for _, s := range m.E2E.Suites {
		key := s.Runner + "/" + s.Label
		if seen[key] {
			return fmt.Errorf("%w: %s", errDuplicateSuite, key)
		}
		seen[key] = true
		if err := s.validate(); err != nil {
			return err
		}
	}
	for _, step := range m.Unit.Steps {
		if err := step.validate(); err != nil {
			return err
		}
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// validateFixtures rejects unknown fixture names and Linux fixtures on other runners.
func (s Suite) validateFixtures() error {
	for _, name := range s.Fixtures {
		if name != fixtureGHFixtureLinux {
			return fmt.Errorf("%w: %s has unknown fixture %q", errInvalidSuite, s.Label, name)
		}
		if s.OS() != osLinux {
			return fmt.Errorf(
				"%w: %s needs a Linux runner for fixture %q",
				errInvalidSuite,
				s.Label,
				name,
			)
		}
	}
	return nil
}
