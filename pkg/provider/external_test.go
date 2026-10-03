package provider

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/hash"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/stretchr/testify/suite"
	"sigs.k8s.io/yaml"
)

const (
	externalFixturePayload = "trusted runtime fixture"
	externalBinaryField    = "agent.external.binary"
	externalChecksumLabel  = "SHA-256"
)

type externalConfigSuite struct{ suite.Suite }

func TestExternalConfig(t *testing.T) { suite.Run(t, new(externalConfigSuite)) }

func externalAgentFixture() ProviderAgentConfig {
	return ProviderAgentConfig{
		Driver: ExternalDriver,
		External: ProviderExternalDriverConfig{
			Binary: "RUNTIME",
			Args:   types.StrArray{"serve", "", "λ spaced", "${STATIC}"},
		},
		Binaries: map[string][]*ProviderBinary{
			"RUNTIME": {
				{
					OS:       runtime.GOOS,
					Arch:     runtime.GOARCH,
					Path:     "https://example.invalid/runtime",
					Checksum: hash.String(externalFixturePayload),
				},
			},
		},
	}
}

func (s *externalConfigSuite) TestManifestValidation() {
	cases := []externalValidationCase{
		{"valid static arguments", func(*ProviderAgentConfig) {}, ""},
		{
			"none image backend",
			func(a *ProviderAgentConfig) { a.External.ImageBackend = "none" },
			"",
		},
		{
			"missing key",
			func(a *ProviderAgentConfig) { a.External.Binary = "" },
			externalBinaryField,
		},
		{
			"path identity",
			func(a *ProviderAgentConfig) { a.External.Binary = "/usr/bin/runtime" },
			externalBinaryField,
		},
		{
			"templated identity",
			func(a *ProviderAgentConfig) { a.External.Binary = "${RUNTIME}" },
			externalBinaryField,
		},
		{
			"undeclared key",
			func(a *ProviderAgentConfig) { a.External.Binary = "MISSING" },
			"agent.binaries",
		},
		{
			"empty locations",
			func(a *ProviderAgentConfig) { a.Binaries["RUNTIME"] = nil },
			"nonempty",
		},
		{"null location", func(a *ProviderAgentConfig) { a.Binaries["RUNTIME"][0] = nil }, "null"},
	}
	s.checkManifests(cases)
}

func (s *externalConfigSuite) TestChecksumValidation() {
	cases := []externalValidationCase{
		{
			"missing checksum",
			func(a *ProviderAgentConfig) { a.Binaries["RUNTIME"][0].Checksum = "" },
			externalChecksumLabel,
		},
		{
			"malformed checksum",
			func(a *ProviderAgentConfig) { a.Binaries["RUNTIME"][0].Checksum = strings.Repeat("z", 64) },
			externalChecksumLabel,
		},
		{
			"short checksum",
			func(a *ProviderAgentConfig) { a.Binaries["RUNTIME"][0].Checksum = "abcd" },
			externalChecksumLabel,
		},
		{"duplicate platform", func(a *ProviderAgentConfig) {
			a.Binaries["RUNTIME"] = append(a.Binaries["RUNTIME"], a.Binaries["RUNTIME"][0])
		}, "duplicate"},
	}
	s.checkManifests(cases)
}

func (s *externalConfigSuite) TestArgumentAndBackendValidation() {
	cases := []externalValidationCase{
		{
			"invalid image backend",
			func(a *ProviderAgentConfig) { a.External.ImageBackend = "unsupported-image-backend" },
			"imageBackend",
		},
		{
			"empty first argument",
			func(a *ProviderAgentConfig) { a.External.Args = types.StrArray{""} },
			"empty argument",
		},
		{
			"NUL argument",
			func(a *ProviderAgentConfig) { a.External.Args = types.StrArray{"serve", "private\x00argument"} },
			"NUL",
		},
	}
	s.checkManifests(cases)
}

type externalValidationCase struct {
	name    string
	mutate  func(*ProviderAgentConfig)
	message string
}

func (s *externalConfigSuite) TestPreparedPaths() {
	for _, local := range []types.StrBool{config.BoolTrue, config.BoolFalse} {
		s.Run(string(local), func() {
			for _, absolute := range []bool{false, true} {
				root := s.T().TempDir()
				agent := externalAgentFixture()
				agent.Local = local
				binary := agent.Binaries["RUNTIME"][0]
				expected := filepath.Join(root, "runtime", "runtime λ fixture")
				binary.Name = "runtime λ fixture"
				if absolute {
					expected = filepath.Join(root, "local λ fixture")
					binary.Path = expected
				}
				s.Require().NoError(os.MkdirAll(filepath.Dir(expected), 0o750))
				s.writeExecutable(expected, externalFixturePayload)
				// An unrelated tool need not be available to resolve the selected runtime.
				agent.Binaries["UNUSED_TOOL"] = nil
				binary.Checksum = strings.ToUpper(binary.Checksum)
				resolved, err := ResolveExternalRuntimeBinary(agent, root)
				s.Require().NoError(err)
				s.Equal(expected, resolved)
			}
		})
	}
}

func (s *externalConfigSuite) TestRelativeLocalPreparedPaths() {
	for _, name := range []string{"", filepath.Join("nested", "runtime")} {
		s.Run(name, func() {
			root := s.T().TempDir()
			agent := externalAgentFixture()
			binary := agent.Binaries["RUNTIME"][0]
			binary.Path = "relative-runtime"
			binary.Name = name
			expected := filepath.Join(root, "runtime", binary.Path)
			if name != "" {
				expected = filepath.Join(root, "runtime", name)
			}
			s.Require().NoError(os.MkdirAll(filepath.Dir(expected), 0o750))
			s.writeExecutable(expected, externalFixturePayload)
			resolved, err := ResolveExternalRuntimeBinary(agent, root)
			s.Require().NoError(err)
			s.Equal(expected, resolved)
		})
	}
}

func (s *externalConfigSuite) TestRelativePathCannotEscape() {
	root := s.T().TempDir()
	agent := externalAgentFixture()
	binary := agent.Binaries["RUNTIME"][0]
	binary.Path = "relative-runtime"
	binary.Name = filepath.Join("..", "outside-runtime")
	outside := filepath.Join(root, "outside-runtime")
	s.writeExecutable(outside, externalFixturePayload)
	resolved, err := ResolveExternalRuntimeBinary(agent, root)
	s.Require().ErrorContains(err, "escapes base directory")
	s.Empty(resolved)
	// #nosec G304 -- This fixture is inside the test temporary directory.
	contents, err := os.ReadFile(outside)
	s.Require().NoError(err)
	s.Equal(externalFixturePayload, string(contents))
}

func (s *externalConfigSuite) TestVerificationPreservesFiles() {
	root := s.T().TempDir()
	agent := externalAgentFixture()
	binary := agent.Binaries["RUNTIME"][0]
	binary.Path = filepath.Join(root, "runtime λ")
	s.writeExecutable(binary.Path, "unexpected content")
	_, err := ResolveExternalRuntimeBinary(agent, root)
	s.Require().ErrorContains(err, "checksum verification failed")
	contents, err := os.ReadFile(binary.Path)
	s.Require().NoError(err)
	s.Equal("unexpected content", string(contents))
	binary.Checksum = ""
	_, err = ResolveExternalRuntimeBinary(agent, root)
	s.Require().ErrorContains(err, externalChecksumLabel)
	s.FileExists(binary.Path)
}

func (s *externalConfigSuite) TestMissingAndWrongPlatform() {
	root := s.T().TempDir()
	agent := externalAgentFixture()
	_, err := ResolveExternalRuntimeBinary(agent, root)
	s.Require().ErrorContains(err, "not prepared")
	s.NoDirExists(filepath.Join(root, "runtime"))
	agent.Binaries["RUNTIME"][0].Arch = "unsupported-agent-arch"
	_, err = ResolveExternalRuntimeBinary(agent, root)
	s.Require().ErrorContains(err, "agent platform")
	agent = externalAgentFixture()
	agent.Binaries["RUNTIME"][0].Path = root
	_, err = ResolveExternalRuntimeBinary(agent, root)
	s.Require().ErrorContains(err, "regular file")
	_, err = ResolveExternalRuntimeBinary(agent, "relative")
	s.Require().ErrorContains(err, "absolute path")
	agent.Driver = DockerDriver
	_, err = ResolveExternalRuntimeBinary(agent, root)
	s.Require().ErrorContains(err, "agent.driver")
}

func (s *externalConfigSuite) TestExecutablePermissions() {
	if runtime.GOOS == "windows" {
		s.T().Skip("Windows execution does not use Unix permission bits")
	}
	if os.Geteuid() == 0 {
		s.T().Skip("Root can execute files with any execute bit")
	}
	for _, mode := range []os.FileMode{0o600, 0o411} {
		root := s.T().TempDir()
		agent := externalAgentFixture()
		path := filepath.Join(root, "runtime")
		agent.Binaries["RUNTIME"][0].Path = path
		s.Require().NoError(os.WriteFile(path, []byte(externalFixturePayload), 0o600))
		s.Require().NoError(os.Chmod(path, mode))
		_, err := ResolveExternalRuntimeBinary(agent, root)
		s.Require().ErrorContains(err, "executable permissions")
		s.FileExists(path)
	}
}

func (s *externalConfigSuite) checkManifests(cases []externalValidationCase) {
	for _, tc := range cases {
		s.Run(tc.name, func() {
			agent := externalAgentFixture()
			tc.mutate(&agent)
			manifest, err := yaml.Marshal(
				&ProviderConfig{
					Name:    "external-fixture",
					Version: "v1.0.0",
					Agent:   agent,
					Exec:    ProviderCommands{Command: types.StrArray{"echo hi"}},
				},
			)
			s.Require().NoError(err)
			parsed, err := ParseProvider(bytes.NewReader(manifest))
			if tc.message != "" {
				s.Require().ErrorContains(err, tc.message)
				return
			}
			s.Require().NoError(err)
			s.Equal(agent.External, parsed.Agent.External)
		})
	}
}

func (s *externalConfigSuite) writeExecutable(path, contents string) {
	s.T().Helper()
	s.Require().NoError(os.WriteFile(path, []byte(contents), 0o600))
	// #nosec G302 -- The runtime fixture must be executable by its owner.
	err := os.Chmod(path, 0o700)
	s.Require().NoError(err)
}
