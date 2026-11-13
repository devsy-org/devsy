package dependencies

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/internal/dependencytest"
	"github.com/stretchr/testify/suite"
)

const (
	surveyTestOption         = "DOCKER_PATH"
	surveyTestSecret         = "private-credential-never-report"
	surveyTestPATH           = "PATH"
	surveyTestEndpointOption = "DOCKER_HOST"
)

type surveySuite struct {
	suite.Suite
	catalog *Catalog
	root    string
}

func TestSurvey(t *testing.T) { suite.Run(t, new(surveySuite)) }

func (s *surveySuite) SetupTest() {
	s.root = s.T().TempDir()
	var err error
	s.catalog, err = LoadCatalog()
	s.Require().NoError(err)
}

func (s *surveySuite) TestFoundMissingAndPassivePreservation() {
	path := s.executable("tools/docker")
	request := s.request("docker", filepath.Dir(path))
	request.Options[surveyTestEndpointOption] = SurveyOption{
		Value: "ssh://user:" + surveyTestSecret + "@host",
	}
	request.Options["DOCKER_ELEVATION"] = SurveyOption{Value: "sudo"}
	dependencytest.Preserve(s.T(), s.root)
	result := s.survey(request)
	s.Require().Len(result.Requirements, 1)
	item := result.Requirements[0]
	s.Equal(surveyAvailable, item.Availability)
	s.Equal(surveyUnknown, item.Compatibility)
	s.Equal(surveyNotChecked, item.Reachability)
	s.Equal(surveyVersionUnknown, item.ReasonCode)
	s.Equal(surveyExternalOnly, item.Policy)
	s.Equal("dependency_recipe_unqualified", item.PolicyReason)
	s.Require().Len(item.Candidates, 1)
	s.Equal(path, item.Candidates[0].LexicalPath)
	s.NotNil(item.Candidates[0].Identity)
	s.False(item.Candidates[0].Executed)
	s.False(item.Executed)
	s.False(result.Executed)
	s.Equal(SurveyManagedCapabilities{}, result.ManagedCapabilities)
	s.Equal(SurveyHost{}, result.Host)
	encoded, err := json.Marshal(result)
	s.Require().NoError(err)
	s.NotContains(string(encoded), surveyTestSecret)
	s.NotContains(string(encoded), "fileIdentity")
	s.NoFileExists(filepath.Join(s.root, "EXECUTED"))
	missing := s.survey(s.request("docker", ""))
	s.Equal(surveyMissing, missing.Requirements[0].Availability)
	s.Equal(LookupMissing, missing.Requirements[0].ReasonCode)
	s.Empty(missing.Requirements[0].Candidates)
}

func (s *surveySuite) TestRecognizedSourcesAndNameSpoofs() {
	for _, binding := range s.catalog.Bindings() {
		request := s.request("docker", "")
		request.Contract = ProviderContract{
			Source:            binding.Source,
			ExecutionContract: binding.ExecutionContract,
		}
		result := s.survey(request)
		s.Equal(binding.ID, result.BindingID)
		s.True(result.ContractVerified)
		s.NotEmpty(result.Requirements)
		s.Equal(SurveyManagedCapabilities{}, result.ManagedCapabilities)
		request.Contract.ExecutionContract = "custom"
		s.Empty(s.survey(request).Requirements)
	}
	for _, name := range []string{toolDocker, toolMicrosandbox} {
		request := s.request("docker", "")
		request.Contract = ProviderContract{
			DisplayName: name,
			Source: ProviderSource{
				Kind:     SourceURL,
				Location: "https://user:" + surveyTestSecret + "@custom",
			},
			ExecutionContract: executionDocker,
		}
		request.Environment.inspect = func(context.Context, string) (lookupFile, error) {
			s.FailNow("untrusted source must not inspect files")
			return lookupFile{}, nil
		}
		result := s.survey(request)
		s.Empty(result.Requirements)
		s.False(result.ContractVerified)
		s.Equal(surveyPolicyBlocked, result.PolicyReason)
		encoded, err := json.Marshal(result)
		s.Require().NoError(err)
		s.NotContains(string(encoded), surveyTestSecret)
	}
}

func (s *surveySuite) TestDefaultAndExplicitOptionIntent() {
	first := s.executable("first/docker")
	second := s.executable("second/docker")
	reviewed := s.executable("reviewed/docker")
	path := strings.Join(
		[]string{filepath.Dir(first), filepath.Dir(second)},
		string(filepath.ListSeparator),
	)
	for _, option := range []SurveyOption{
		{Value: toolDocker, Origin: SurveyDefault},
		{Value: toolDocker, Origin: SurveyDefault, UserProvided: true},
		{Value: toolDocker, Origin: LookupExplicit},
		{Value: toolDocker, Origin: LookupExplicitEnv},
	} {
		request := s.request("docker", path)
		request.Options[surveyTestOption] = option
		request.ReviewedLocations[toolDocker] = []string{reviewed}
		item := s.survey(request).Requirements[0]
		if option.Origin == SurveyDefault && !option.UserProvided {
			s.Len(item.Candidates, 3)
			s.Equal(LookupPATH, item.Candidates[0].Origin)
		} else {
			s.Require().Len(item.Candidates, 1)
			origin := LookupExplicit
			if option.Origin == LookupExplicitEnv {
				origin = LookupExplicitEnv
			}
			s.Equal(origin, item.Candidates[0].Origin)
		}
		s.Equal(first, item.Candidates[0].LexicalPath)
	}
}

func (s *surveySuite) TestEmptyInvalidAndMissingExplicitNeverFallback() {
	fallback := s.executable("fallback/docker")
	values := []string{
		"",
		"missing",
		filepath.Join(s.root, "absent"),
		"bad\x00path",
		"https://" + surveyTestSecret + "@host",
	}
	for _, value := range values {
		for _, origin := range []LookupOrigin{SurveyDefault, LookupExplicit, LookupExplicitEnv} {
			request := s.request("docker", filepath.Dir(fallback))
			request.Options[surveyTestOption] = SurveyOption{Value: value, Origin: origin}
			request.ReviewedLocations[toolDocker] = []string{fallback}
			item := s.survey(request).Requirements[0]
			s.Equal(LookupInvalidPath, item.ReasonCode)
			for _, candidate := range item.Candidates {
				s.NotEqual(fallback, candidate.LexicalPath)
				s.NotContains(candidate.LexicalPath, surveyTestSecret)
			}
		}
	}
}

func (s *surveySuite) TestReviewedLocationsAndExactPaths() {
	selected := s.executable(" spaced \u96ea/tools;literal ")
	fallback := s.executable("fallback/docker")
	request := s.request("docker", "")
	request.ReviewedLocations[toolDocker] = []string{selected}
	item := s.survey(request).Requirements[0]
	s.Require().Len(item.Candidates, 1)
	s.Equal(selected, item.Candidates[0].LexicalPath)
	s.Equal(LookupReviewed, item.Candidates[0].Origin)
	request = s.request("docker", filepath.Dir(fallback))
	request.Options[surveyTestOption] = SurveyOption{Value: selected, UserProvided: true}
	request.ReviewedLocations[toolDocker] = []string{fallback}
	item = s.survey(request).Requirements[0]
	s.Require().Len(item.Candidates, 1)
	s.Equal(selected, item.Candidates[0].LexicalPath)
	s.Equal(LookupExplicit, item.Candidates[0].Origin)
}

func (s *surveySuite) TestNativePathContainingURIDelimiter() {
	if runtime.GOOS == lookupWindows {
		s.T().Skip("Unix path with a colon directory")
	}
	path := s.executable("tools:/docker")
	path = strings.Replace(path, "tools:/", "tools://", 1)
	request := s.request("docker", "")
	request.Options[surveyTestOption] = SurveyOption{Value: path, UserProvided: true}
	dependencytest.Preserve(s.T(), s.root)
	observed, err := request.Environment.LookupExecutable(context.Background(), LookupRequest{
		Command: path, Explicit: true,
	})
	s.Require().NoError(err)
	s.Require().Len(observed.Candidates, 1)
	s.Equal(FileExecutable, observed.Candidates[0].State)
	item := s.survey(request).Requirements[0]
	s.Equal(surveyAvailable, item.Availability)
	s.Equal(surveyVersionUnknown, item.ReasonCode)
	s.Require().Len(item.Candidates, 1)
	s.Equal(path, item.Candidates[0].LexicalPath)
	s.True(observed.Candidates[0].Identity.SameFile(item.Candidates[0].Identity))
	s.NoFileExists(filepath.Join(s.root, "EXECUTED"))
}

func (s *surveySuite) TestWindowsNativePathContainingURIDelimiter() {
	if runtime.GOOS != lookupWindows {
		s.T().Skip("requires native Windows drive path semantics")
	}
	path := filepath.ToSlash(s.executable("tools/docker"))
	volume := filepath.VolumeName(path)
	s.Require().NotEmpty(volume)
	path = volume + "//" + strings.TrimPrefix(path, volume+"/")
	request := s.request("docker", "")
	request.Options[surveyTestOption] = SurveyOption{Value: path, UserProvided: true}
	dependencytest.Preserve(s.T(), s.root)
	observed, err := request.Environment.LookupExecutable(context.Background(), LookupRequest{
		Command: path, Explicit: true,
	})
	s.Require().NoError(err)
	s.Require().Len(observed.Candidates, 1)
	s.Equal(FileExecutable, observed.Candidates[0].State)
	item := s.survey(request).Requirements[0]
	s.Equal(surveyAvailable, item.Availability)
	s.Require().Len(item.Candidates, 1)
	s.Equal(path, item.Candidates[0].LexicalPath)
	s.True(observed.Candidates[0].Identity.SameFile(item.Candidates[0].Identity))
}

func (s *surveySuite) TestCatalogPlatformPolicySyntheticTuples() {
	// Synthetic policy inputs exercise catalog exclusions, not native OS lookup.
	for _, binding := range s.catalog.Bindings() {
		match, err := s.catalog.Match(ProviderContract{
			Source: binding.Source, ExecutionContract: binding.ExecutionContract,
		})
		s.Require().NoError(err)
		for _, platform := range []string{
			"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64",
			"windows/amd64", "windows/arm64",
		} {
			expected := "dependency_platform_unsupported"
			if slices.Contains(binding.Platforms, platform) {
				expected = match.ManagedReason
			}
			s.Equal(
				expected,
				s.catalog.surveyPolicyReason(match, platform),
				binding.ID+" on "+platform,
			)
		}
		match.ContractVerified = false
		match.ManagedReason = surveyPolicyBlocked
		s.Equal(surveyPolicyBlocked, s.catalog.surveyPolicyReason(match, "other/unknown"))
	}
}

func (s *surveySuite) TestNativeCatalogPlatformPolicy() {
	platform := runtime.GOOS + "/" + runtime.GOARCH
	path := s.executable("native/tool")
	dependencytest.Preserve(s.T(), s.root)
	for _, binding := range s.catalog.Bindings() {
		request := s.request("docker", "")
		request.Contract = ProviderContract{
			Source: binding.Source, ExecutionContract: binding.ExecutionContract,
		}
		if binding.Requirements[0].ConfigurationOption == "" {
			request.ReviewedLocations[binding.Requirements[0].ToolID] = []string{path}
		}
		before := s.survey(request)
		request.Host = SurveyHost{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
		after := s.survey(request)
		s.Equal(before.PolicyReason, after.PolicyReason)
		s.Empty(after.Host.NativeArchitecture)
		s.Empty(after.Host.VirtualizationCapability)
		s.Equal(SurveyManagedCapabilities{}, after.ManagedCapabilities)
		item := after.Requirements[0]
		if binding.Requirements[0].ConfigurationOption == "" {
			s.Equal(surveyAvailable, item.Availability)
		}
		if slices.Contains(binding.Platforms, platform) {
			s.Equal("dependency_recipe_unqualified", after.PolicyReason)
			s.Equal(surveyExternalOnly, item.Policy)
		} else {
			s.Equal("dependency_platform_unsupported", after.PolicyReason)
			s.Equal(after.PolicyReason, item.PolicyReason)
			s.Equal("unsupported", item.Policy)
		}
	}
}

func (s *surveySuite) TestConditionalBackendAndGuestIsolation() {
	msb := s.executable("runtime/msb")
	for _, backend := range []string{"", toolDocker, imageBackendNone} {
		request := s.externalRequest(toolMicrosandbox, "external_microsandbox_v1")
		var err error
		request.Environment, err = NewLookupEnvironment(
			map[string]string{surveyTestPATH: filepath.Dir(msb)},
			s.root,
		)
		s.Require().NoError(err)
		request.Contract.ImageBackend = backend
		request.Contract.AdapterVersion = "0.1.11"
		request.Options["DockerPath"] = SurveyOption{Value: surveyTestSecret, UserProvided: true}
		request.Options["MICROSANDBOX_PATH"] = SurveyOption{
			Value:        surveyTestSecret,
			UserProvided: true,
		}
		result := s.survey(request)
		count := 2
		if backend == imageBackendNone {
			count = 1
		}
		s.Require().Len(result.Requirements, count)
		s.Equal(toolMicrosandbox, result.Requirements[0].Requirement.ToolID)
		s.Equal(surveyAvailable, result.Requirements[0].Availability)
		s.Equal(surveyUnknown, result.Requirements[0].Compatibility)
		s.Equal(surveyVersionUnknown, result.Requirements[0].ReasonCode)
		s.Equal(SurveyManagedCapabilities{}, result.ManagedCapabilities)
		s.Equal("version_unqualified", result.AdapterStatus)
		if count == 2 {
			s.Equal(toolDocker, result.Requirements[1].Requirement.ToolID)
			s.Equal("image_backend", result.Requirements[1].Requirement.Purpose)
			s.Empty(result.Requirements[1].Requirement.ExecutableOption)
		}
	}
	for _, provider := range []string{toolLima, toolOrbStack} {
		request := s.externalRequest(provider, provider+"_exec_v1")
		request.Options[surveyTestOption] = SurveyOption{
			Value:        surveyTestSecret,
			UserProvided: true,
		}
		result := s.survey(request)
		s.Require().Len(result.Requirements, 1)
		s.Equal(provider, result.Requirements[0].Requirement.ToolID)
		s.Equal("controller_host", result.Requirements[0].Requirement.Scope)
	}
}

func (s *surveySuite) TestKubernetesConfigurationIsInert() {
	credential := s.executable("credentials/credential-plugin")
	request := s.request("kubernetes", filepath.Dir(credential))
	request.Options["KUBERNETES_CONFIG"] = SurveyOption{
		Value: "users:\n- user:\n    exec:\n      command: " + credential + "\n      args: [" + surveyTestSecret + "]",
	}
	request.Environment.inspect = func(context.Context, string) (lookupFile, error) {
		s.FailNow("configuration guidance must not look up files")
		return lookupFile{}, nil
	}
	dependencytest.Preserve(s.T(), s.root)
	result := s.survey(request)
	s.Require().Len(result.Requirements, 1)
	item := result.Requirements[0]
	s.Equal("KUBERNETES_CONFIG", item.Requirement.ConfigurationOption)
	s.Equal(surveyUnknown, item.Availability)
	s.Equal(surveyPolicyBlocked, item.ReasonCode)
	s.Equal(surveyNotChecked, item.Reachability)
	s.Empty(item.Candidates)
	s.NoFileExists(filepath.Join(s.root, "EXECUTED"))
	encoded, err := json.Marshal(result)
	s.Require().NoError(err)
	s.NotContains(string(encoded), surveyTestSecret)
	s.NotContains(string(encoded), credential)
}

func (s *surveySuite) TestInputOutputAndRevisionIsolation() {
	path := s.executable("reviewed/docker")
	request := s.request("docker", "")
	request.Target = SurveyTarget{
		StoreID:                "store-1",
		ContextName:            "context-1",
		ProviderName:           "provider-1",
		ProviderFingerprint:    "fingerprint-1",
		ConfigRevision:         "config-1",
		ProviderConfigRevision: "provider-config-1",
	}
	request.ReviewedLocations[toolDocker] = []string{path}
	before := s.survey(request)
	s.Equal(request.Target, before.Target)
	s.Equal(s.catalog.ID(), before.CatalogID)
	s.Equal(s.catalog.Hash(), before.CatalogHash)
	before.Requirements[0].Requirement.RequiredFor[0] = "mutated-operation"
	before.Requirements[0].Candidates[0].LexicalPath = "mutated-candidate-path"
	after := s.survey(request)
	s.Equal("create", after.Requirements[0].Requirement.RequiredFor[0])
	s.Equal(path, after.Requirements[0].Candidates[0].LexicalPath)
	request.ReviewedLocations[toolDocker][0] = filepath.Join(s.root, "missing")
	request.Target.ConfigRevision = "config-2"
	request.Target.ProviderConfigRevision = "provider-config-2"
	changed := s.survey(request)
	s.NotEqual(after.Target, changed.Target)
	s.Empty(changed.Requirements[0].Candidates)
	s.Equal(path, after.Requirements[0].Candidates[0].LexicalPath)
	s.Equal("config-1", after.Target.ConfigRevision)
}

func (s *surveySuite) TestEffectiveEnvironmentSnapshotIsImmutable() {
	path := s.executable("supplied/docker")
	values := map[string]string{
		surveyTestPATH:           filepath.Dir(path),
		surveyTestEndpointOption: surveyTestSecret,
	}
	request := s.request("docker", "")
	var err error
	request.Environment, err = NewLookupEnvironment(values, s.root)
	s.Require().NoError(err)
	values[surveyTestPATH] = s.root
	delete(values, surveyTestEndpointOption)
	dependencytest.Preserve(s.T(), s.root)
	result := s.survey(request)
	s.Require().Len(result.Requirements[0].Candidates, 1)
	s.Equal(path, result.Requirements[0].Candidates[0].LexicalPath)
	encoded, err := json.Marshal(result)
	s.Require().NoError(err)
	s.NotContains(string(encoded), surveyTestSecret)
}

func (s *surveySuite) request(provider, path string) SurveyRequest {
	environment, err := NewLookupEnvironment(map[string]string{surveyTestPATH: path}, s.root)
	s.Require().NoError(err)
	return SurveyRequest{
		Contract: ProviderContract{
			Source:            ProviderSource{Kind: SourceInternal, InternalID: provider},
			ExecutionContract: "internal_" + provider,
		},
		Environment:       environment,
		Options:           map[string]SurveyOption{},
		ReviewedLocations: map[string][]string{},
	}
}

func (s *surveySuite) externalRequest(provider, contract string) SurveyRequest {
	request := s.request("docker", "")
	request.Contract = ProviderContract{
		Source: ProviderSource{
			Kind:       SourceGitHub,
			Repository: "github.com/devsy-org/devsy-provider-" + provider,
		},
		ExecutionContract: contract,
	}
	return request
}

func (s *surveySuite) survey(request SurveyRequest) SurveyResult {
	result, err := s.catalog.Survey(context.Background(), request)
	s.Require().NoError(err)
	return result
}

func (s *surveySuite) executable(relative string) string {
	path := filepath.Join(s.root, relative)
	if runtime.GOOS == lookupWindows {
		path += ".EXE"
	}
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o700))
	marker := strings.ReplaceAll(filepath.Join(s.root, "EXECUTED"), "'", "'\\''")
	s.Require().
		NoError(os.WriteFile(path, []byte("#!/bin/sh\nprintf executed > '"+marker+"'\n"), 0o600))
	info, err := os.Stat(path)
	s.Require().NoError(err)
	s.Require().NoError(os.Chmod(path, info.Mode()|0o100))
	return path
}
