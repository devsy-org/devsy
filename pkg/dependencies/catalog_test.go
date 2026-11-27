package dependencies

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

type catalogSuite struct {
	suite.Suite
}

const (
	testCustom = "custom"
	testShell  = "shell"

	platLinuxAMD   = "linux/amd64"
	platLinuxARM   = "linux/arm64"
	platDarwinAMD  = "darwin/amd64"
	platDarwinARM  = "darwin/arm64"
	platWindowsAMD = "windows/amd64"
	platWindowsARM = "windows/arm64"
)

func TestCatalog(t *testing.T) {
	suite.Run(t, new(catalogSuite))
}

func (s *catalogSuite) TestInternalIdentityCannotBeInheritedByDisplayName() {
	catalog, err := LoadCatalog()
	s.Require().NoError(err)
	for _, binding := range catalog.Bindings()[:4] {
		s.Run(binding.ID, func() {
			match, matchErr := catalog.Match(ProviderContract{
				DisplayName: "renamed-provider", Source: binding.Source,
				ExecutionContract: binding.ExecutionContract,
			})
			s.Require().NoError(matchErr)
			s.Equal(binding.ID, match.BindingID)
			s.True(match.ContractVerified)
			s.False(match.ManagedEligible)
			custom, customErr := catalog.Match(ProviderContract{
				DisplayName:       binding.ID,
				Source:            ProviderSource{Kind: SourceFile, Location: "/tmp/provider.yaml"},
				ExecutionContract: binding.ExecutionContract,
			})
			s.Require().NoError(customErr)
			s.Empty(custom.BindingID)
			s.Empty(custom.Requirements)
			s.False(custom.ManagedEligible)
		})
	}
}

func (s *catalogSuite) TestCanonicalExternalAliasesAndAdapterFacts() {
	catalog, err := LoadCatalog()
	s.Require().NoError(err)
	for _, binding := range catalog.Bindings()[4:] {
		s.Run(binding.ID, func() {
			for _, version := range []string{"", "v0.1.11", "0.0.1", "99.0.0"} {
				match, matchErr := catalog.Match(ProviderContract{
					DisplayName: "my-alias", Source: binding.Source, AdapterVersion: version,
					ExecutionContract: binding.ExecutionContract, RuntimeCheckSchema: 1,
					ManifestSHA256: strings.Repeat("a", 64), ImageBackend: imageBackendNone,
				})
				s.Require().NoError(matchErr)
				s.True(match.ContractVerified)
				s.Equal(binding.ID, match.BindingID)
				s.Len(match.Requirements, 1)
				s.False(match.ManagedEligible)
				s.Equal("dependency_recipe_unqualified", match.ManagedReason)
				if version == "" {
					s.Equal("version_unknown", match.AdapterStatus)
				} else {
					s.Equal("version_unqualified", match.AdapterStatus)
				}
			}
		})
	}
}

func (s *catalogSuite) TestKnownOriginWithoutExecutionContractHasGuidanceOnly() {
	catalog, err := LoadCatalog()
	s.Require().NoError(err)
	for _, execution := range []string{"", testCustom, executionDocker} {
		match, matchErr := catalog.Match(ProviderContract{
			Source: externalSource(toolMicrosandbox), ExecutionContract: execution,
		})
		s.Require().NoError(matchErr)
		s.Equal(toolMicrosandbox, match.BindingID)
		s.Equal("microsandbox_setup", match.GuidanceID)
		s.Empty(match.Requirements)
		s.False(match.ContractVerified)
		s.False(match.ManagedEligible)
	}
}

func (s *catalogSuite) TestLookalikesAndCanonicalLookingURLsAreUnmanaged() {
	catalog, err := LoadCatalog()
	s.Require().NoError(err)
	sources := []ProviderSource{
		{Kind: SourceGitHub, Repository: "github.com/evil/devsy-provider-microsandbox"},
		{Kind: SourceGitHub, Repository: "github.com/devsy-org/devsy-provider-microsandbox-evil"},
		{Kind: SourceGitHub, Repository: "github.com/devsy-org/devsy-provider-microsandbox.git"},
		{Kind: SourceURL, Location: "https://github.com/devsy-org/devsy-provider-microsandbox"},
		{
			Kind:     SourceURL,
			Location: "https://github.com.evil/devsy-org/devsy-provider-microsandbox",
		},
		{Kind: SourceInternal, InternalID: toolMicrosandbox},
	}
	for _, source := range sources {
		match, matchErr := catalog.Match(ProviderContract{
			DisplayName:       toolMicrosandbox,
			Source:            source,
			ExecutionContract: executionMicrosandbox,
		})
		s.Require().NoError(matchErr)
		s.Empty(match.BindingID)
		s.Empty(match.Requirements)
		s.False(match.ManagedEligible)
	}
}

func (s *catalogSuite) TestMalformedAndConflictingSourceFactsFailClosed() {
	catalog, err := LoadCatalog()
	s.Require().NoError(err)
	cases := []ProviderContract{
		{
			Source: ProviderSource{
				Kind:       SourceGitHub,
				Repository: externalSource(toolLima).Repository,
				InternalID: toolDocker,
			},
		},
		{
			Source: ProviderSource{
				Kind:       SourceInternal,
				InternalID: toolDocker,
				Location:   "/tmp/provider.yaml",
			},
		},
		{Source: ProviderSource{Kind: testCustom, InternalID: toolDocker}},
		{
			Source: ProviderSource{
				Kind:       SourceGitHub,
				Repository: externalSource(toolLima).Repository + "/suffix",
			},
		},
		{
			Source: ProviderSource{
				Kind:       SourceGitHub,
				Repository: "https://github.com/devsy-org/devsy-provider-lima",
			},
		},
		{
			Source: ProviderSource{
				Kind:       SourceGitHub,
				Repository: externalSource(toolLima).Repository + "@v1.1.0",
			},
		},
		{Source: externalSource(toolLima), AdapterVersion: "latest"},
		{Source: externalSource(toolLima), AdapterVersion: "1.1"},
		{Source: externalSource(toolLima), ManifestSHA256: "abc"},
		{Source: externalSource(toolLima), RuntimeCheckSchema: -1},
		{Source: externalSource(toolLima), ImageBackend: testCustom},
	}
	for i, contract := range cases {
		match, matchErr := catalog.Match(contract)
		s.Error(matchErr, "case %d", i)
		s.False(match.ManagedEligible)
		s.Empty(match.Requirements)
	}
}

func (s *catalogSuite) TestMicroSandboxImageBackendIsConditional() {
	catalog, err := LoadCatalog()
	s.Require().NoError(err)
	contract := ProviderContract{
		Source:            externalSource(toolMicrosandbox),
		ExecutionContract: executionMicrosandbox,
	}
	for _, backend := range []string{"", imageBackendNone, toolDocker} {
		contract.ImageBackend = backend
		match, matchErr := catalog.Match(contract)
		s.Require().NoError(matchErr)
		s.Equal(toolMicrosandbox, match.Requirements[0].ToolID)
		s.Empty(match.Requirements[0].ExecutableOption)
		if backend != imageBackendNone {
			s.Len(match.Requirements, 2)
			s.Equal("image_backend", match.Requirements[1].Purpose)
			s.Empty(match.Requirements[1].ExecutableOption)
		} else {
			s.Len(match.Requirements, 1)
		}
	}
}

// Each list comes from a release or host check, not from a guess. The internal
// Docker, Podman and Kubernetes providers ship with the Devsy CLI, which builds
// linux, darwin and windows for amd64 and arm64. The Apple driver refuses any
// host except darwin/arm64. The external providers publish what their release
// builds produce: Lima linux and darwin on both architectures, OrbStack darwin
// on both, and MicroSandbox linux on both, darwin/arm64 and windows/amd64.
func (s *catalogSuite) TestBindingPlatformsMatchTheirSources() {
	allDesktop := []string{
		platLinuxAMD, platLinuxARM, platDarwinAMD, platDarwinARM, platWindowsAMD, platWindowsARM,
	}
	want := map[string][]string{
		"docker":       allDesktop,
		"podman":       allDesktop,
		"kubernetes":   allDesktop,
		"apple":        {platDarwinARM},
		"lima":         {platLinuxAMD, platLinuxARM, platDarwinAMD, platDarwinARM},
		"orbstack":     {platDarwinAMD, platDarwinARM},
		"microsandbox": {platLinuxAMD, platLinuxARM, platDarwinARM, platWindowsAMD},
	}
	catalog, err := LoadCatalog()
	s.Require().NoError(err)

	got := map[string][]string{}
	for _, binding := range catalog.Bindings() {
		got[binding.ID] = binding.Platforms
	}

	s.Equal(want, got)
}

type bindingContract struct {
	source              ProviderSource
	execution, guidance string
	schema              int
	requirements        []string
}

func internalSource(id string) ProviderSource {
	return ProviderSource{Kind: SourceInternal, InternalID: id}
}

// The validator checks shape only, so the per-provider facts live here. A
// requirement reads as key|operations|executable option|configuration option|
// probe|image backend condition.
func expectedContracts() map[string]bindingContract {
	const operations = "create,exec,stop,delete"
	return map[string]bindingContract{
		"docker": {
			internalSource("docker"), "internal_docker", "docker_setup", 0,
			[]string{"docker:container_runtime|" + operations + "|DOCKER_PATH||docker_version|"},
		},
		"podman": {
			internalSource("podman"), "internal_podman", "podman_setup", 0,
			[]string{"podman:container_runtime|" + operations + "|PODMAN_PATH||podman_version|"},
		},
		"apple": {
			internalSource("apple"),
			"internal_apple",
			"apple_container_setup",
			0,
			[]string{
				"apple-container:container_runtime|" + operations + "|CONTAINER_PATH||container_version|",
			},
		},
		"kubernetes": {
			internalSource("kubernetes"),
			"internal_kubernetes",
			"kubernetes_configuration",
			0,
			[]string{
				"kubernetes-config:cluster_configuration|" + operations + "||KUBERNETES_CONFIG||",
			},
		},
		"microsandbox": {
			externalSource(toolMicrosandbox), executionMicrosandbox, "microsandbox_setup", 1,
			[]string{
				"microsandbox:microvm_runtime|" + operations + "|||microsandbox_version|",
				"docker:image_backend|create,image-build|||docker_version|docker",
			},
		},
		"lima": {
			externalSource(toolLima), "lima_exec_v1", "lima_setup", 0,
			[]string{"lima:vm_runtime|" + operations + "|LIMA_PATH||limactl_version|"},
		},
		"orbstack": {
			externalSource(toolOrbStack), "orbstack_exec_v1", "orbstack_setup", 0,
			[]string{"orbstack:vm_runtime|" + operations + "|ORBSTACK_PATH||orbctl_version|"},
		},
	}
}

func (s *catalogSuite) TestBindingContractsMatchTheirProviders() {
	catalog, err := LoadCatalog()
	s.Require().NoError(err)

	got := map[string]bindingContract{}
	for _, binding := range catalog.Bindings() {
		entry := bindingContract{
			binding.Source, binding.ExecutionContract, binding.GuidanceID,
			binding.RequiredRuntimeCheckSchema, nil,
		}
		for _, r := range binding.Requirements {
			entry.requirements = append(entry.requirements, strings.Join([]string{
				r.ToolID + ":" + r.Purpose, strings.Join(r.RequiredFor, ","), r.ExecutableOption,
				r.ConfigurationOption, r.ProbeID, r.WhenImageBackend,
			}, "|"))
		}
		got[binding.ID] = entry
	}

	s.Equal(expectedContracts(), got)
}

func (s *catalogSuite) TestKubernetesHasNoExecutableProbeOrKubectlDependency() {
	catalog, err := LoadCatalog()
	s.Require().NoError(err)
	match, err := catalog.Match(ProviderContract{
		Source: ProviderSource{
			Kind:       SourceInternal,
			InternalID: "kubernetes",
		},
		ExecutionContract: "internal_kubernetes",
	})
	s.Require().NoError(err)
	s.Require().Len(match.Requirements, 1)
	s.Equal("kubernetes-config", match.Requirements[0].ToolID)
	s.Equal("KUBERNETES_CONFIG", match.Requirements[0].ConfigurationOption)
	s.Empty(match.Requirements[0].ExecutableOption)
	s.Empty(match.Requirements[0].ProbeID)
}

func (s *catalogSuite) TestCatalogIdentityAndCopies() {
	catalog, err := LoadCatalog()
	s.Require().NoError(err)
	s.Equal("devsy.dependencies.catalog.v1", catalog.ID())
	s.Equal("no_qualified_recipe", catalog.ManagedStatus())
	var compact bytes.Buffer
	s.Require().NoError(json.Compact(&compact, []byte(embeddedCatalog)))
	copyCatalog, err := decodeCatalog(compact.Bytes())
	s.Require().NoError(err)
	s.Equal(catalog.Hash(), copyCatalog.Hash())
	bindings := catalog.Bindings()
	bindings[0].AdapterSemverRange = ">=0.0.0"
	bindings[0].Source = externalSource(toolLima)
	bindings[0].Platforms[0] = "evil/platform"
	bindings[0].Requirements[0].RequiredFor[0] = testShell
	bindings[0].Requirements[0].ExecutableOption = "EVIL_PATH"
	match, err := catalog.Match(ProviderContract{
		Source: ProviderSource{
			Kind:       SourceInternal,
			InternalID: toolDocker,
		},
		ExecutionContract: executionDocker,
	})
	s.Require().NoError(err)
	match.Requirements[0].RequiredFor[0] = testShell
	s.Equal("create", catalog.Bindings()[0].Requirements[0].RequiredFor[0])
	s.Equal("DOCKER_PATH", catalog.Bindings()[0].Requirements[0].ExecutableOption)
	s.False(match.ManagedEligible)
	s.Equal(copyCatalog.Bindings(), catalog.Bindings())
	match.ManagedEligible = true
	s.True(match.ManagedEligible)
	unchanged, err := catalog.Match(ProviderContract{
		Source: ProviderSource{
			Kind:       SourceInternal,
			InternalID: toolDocker,
		},
		ExecutionContract: executionDocker,
	})
	s.Require().NoError(err)
	s.False(unchanged.ManagedEligible)
}
