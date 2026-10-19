package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/internal/dependencytest"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/dependencies"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/providers"
	"github.com/stretchr/testify/suite"
)

const (
	dependencyAdapterOption           = "DOCKER_PATH"
	dependencyAdapterSecret           = "private-adapter-test-value"
	dependencyAdapterMicrosandbox     = "microsandbox"
	dependencyAdapterMicrosandboxRepo = "devsy-org/devsy-provider-microsandbox"
	dependencyAdapterElevation        = "pkexec"
)

type providerDependencySurveySuite struct {
	suite.Suite
	root string
	bin  string
}

func TestProviderDependencySurveySuite(t *testing.T) {
	suite.Run(t, new(providerDependencySurveySuite))
}

func (s *providerDependencySurveySuite) SetupTest() {
	s.root = s.T().TempDir()
	s.bin = filepath.Join(s.root, "tools")
	s.Require().NoError(os.Mkdir(s.bin, 0o700))
	for _, name := range []string{
		DefaultDockerCommand, providerDependencyPodman, defaultContainerCommand,
		"kubectl", "exec-auth", dependencyAdapterElevation, "sudo", "doas",
	} {
		s.executable(name)
	}
	s.T().Setenv(config.EnvHome, filepath.Join(s.root, "must-not-create-devsy-home"))
	s.T().Setenv("PATH", s.bin)
}

func (s *providerDependencySurveySuite) TestBuiltInsAndRenamedInstallation() {
	for _, source := range []string{
		DefaultDockerCommand, providerDependencyPodman, providerDependencyApple, providerDependencyKubernetes,
	} {
		request := s.request(source)
		request.Provider.Name = "my-" + source
		request.Target.ProviderName = request.Provider.Name
		result := s.survey(request)
		s.True(result.ContractVerified, source)
		s.Equal(source, result.BindingID)
		s.NotEmpty(result.CatalogHash)
		s.Require().Len(result.Requirements, 1)
		item := result.Requirements[0]
		s.False(item.Executed)
		s.Equal("unknown", item.Compatibility)
		s.Equal("not_checked", item.Reachability)
		if source == providerDependencyKubernetes {
			s.Equal("kubernetes-config", item.Requirement.ToolID)
			s.Empty(item.Candidates)
			continue
		}
		s.Equal("available", item.Availability)
		s.Require().Len(item.Candidates, 1)
		s.Equal(dependencies.LookupPATH, item.Candidates[0].Origin)
		if source == providerDependencyApple &&
			(runtime.GOOS != "darwin" || runtime.GOARCH != "arm64") {
			s.Equal("unsupported", item.Policy)
			s.Equal("dependency_platform_unsupported", result.PolicyReason)
		}
	}
}

func (s *providerDependencySurveySuite) TestEffectiveOptionIntent() {
	path := filepath.Join(s.bin, DefaultDockerCommand)
	if runtime.GOOS == execTestWindowsOS {
		path += ".exe"
	}
	for _, test := range []struct {
		name     string
		present  bool
		value    config.OptionValue
		origin   dependencies.LookupOrigin
		expected dependencies.LookupOrigin
	}{
		{name: "absent", expected: dependencies.LookupPATH},
		{
			name: "default-value", present: true, value: config.OptionValue{Value: DefaultDockerCommand},
			expected: dependencies.LookupPATH,
		},
		{
			name: "user-same-default", present: true, value: config.OptionValue{Value: DefaultDockerCommand, UserProvided: true},
			expected: dependencies.LookupExplicit,
		},
		{
			name: "option-same-default", present: true, value: config.OptionValue{Value: DefaultDockerCommand},
			origin: dependencies.LookupExplicit, expected: dependencies.LookupExplicit,
		},
		{
			name: "env-same-default", present: true, value: config.OptionValue{Value: DefaultDockerCommand},
			origin: dependencies.LookupExplicitEnv, expected: dependencies.LookupExplicitEnv,
		},
		{
			name: "explicit-path", present: true, value: config.OptionValue{Value: path, UserProvided: true},
			expected: dependencies.LookupExplicit,
		},
		{
			name: "inherited-path", present: true, value: config.OptionValue{Value: path},
			expected: dependencies.LookupExplicit,
		},
		{name: "empty", present: true},
		{name: "explicit-empty", present: true, value: config.OptionValue{UserProvided: true}},
	} {
		request := s.request(DefaultDockerCommand)
		// Raw environment keys cannot establish option origin or override an effective option snapshot.
		request.Environment[dependencyAdapterOption] = filepath.Join(s.root, "wrong-docker")
		if test.present {
			request.Options[dependencyAdapterOption] = test.value
			request.OptionOrigins[dependencyAdapterOption] = test.origin
		}
		item := s.survey(request).Requirements[0]
		if test.present && test.value.Value == "" {
			s.Equal(dependencies.LookupInvalidPath, item.ReasonCode, test.name)
			s.Empty(item.Candidates, test.name)
			continue
		}
		s.Require().Len(item.Candidates, 1, test.name)
		s.Equal(test.expected, item.Candidates[0].Origin, test.name)
		s.Equal(path, item.Candidates[0].LexicalPath, test.name)
	}
}

func (s *providerDependencySurveySuite) TestNoAmbientPATHOrEndpointValues() {
	request := s.request(DefaultDockerCommand)
	delete(request.Environment, "PATH")
	endpoint := "ssh://user:" + dependencyAdapterSecret + "@127.0.0.1:1/runtime"
	for _, name := range []string{
		"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_BUILDER", "DOCKER_TLS_VERIFY", "DOCKER_CONFIG",
	} {
		request.Environment[name] = endpoint
		request.Options[name] = config.OptionValue{
			Value:        endpoint,
			UserProvided: true,
			Children:     []string{"child"},
		}
	}
	request.Options["DOCKER_ELEVATION"] = config.OptionValue{
		Value:        dependencyAdapterElevation,
		UserProvided: true,
	}
	result := s.survey(request)
	s.Empty(result.Requirements[0].Candidates)
	s.Equal("missing", result.Requirements[0].Availability)
	encoded, err := json.Marshal(result)
	s.Require().NoError(err)
	s.NotContains(string(encoded), dependencyAdapterSecret)
	s.NotContains(string(encoded), "ssh://")
}

func (s *providerDependencySurveySuite) executable(name string) string {
	if runtime.GOOS == execTestWindowsOS {
		name += ".exe"
	}
	path := filepath.Join(s.bin, name)
	body := "#!/bin/sh\nprintf executed > '" + filepath.Join(s.root, "executed") + "'\nexit 97\n"
	s.Require().NoError(os.WriteFile(path, []byte(body), 0o600))
	// #nosec G302 -- temporary sentinels need execute bits to detect accidental invocation.
	s.Require().NoError(os.Chmod(path, 0o700))
	return path
}

func (s *providerDependencySurveySuite) request(source string) ProviderDependencySurveyRequest {
	manifest, err := provider.ParseProvider(
		strings.NewReader(providers.GetBuiltInProviders()[source]),
	)
	s.Require().NoError(err)
	manifest.Source = provider.ProviderSource{Internal: true, Raw: source}
	return ProviderDependencySurveyRequest{
		Provider:      *manifest,
		Options:       map[string]config.OptionValue{},
		OptionOrigins: map[string]dependencies.LookupOrigin{},
		Environment: map[string]string{
			"PATH":    s.bin,
			"PATHEXT": ".EXE",
		},
		WorkingDirectory: s.root,
		Target: dependencies.SurveyTarget{
			StoreID: "store", ContextName: "context", ProviderName: manifest.Name,
			ProviderFingerprint: "manifest-revision", ConfigRevision: "config-revision",
			ProviderConfigRevision: "provider-revision",
		},
		Host: dependencies.SurveyHost{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH},
	}
}

func (s *providerDependencySurveySuite) survey(
	request ProviderDependencySurveyRequest,
) dependencies.SurveyResult {
	dependencytest.Preserve(s.T(), s.root)
	before, err := json.Marshal(request)
	s.Require().NoError(err)
	result, err := SurveyProviderDependencies(context.Background(), request)
	s.Require().NoError(err)
	after, err := json.Marshal(request)
	s.Require().NoError(err)
	s.Equal(string(before), string(after), "caller snapshots changed")
	s.Equal(request.Target, result.Target)
	s.Equal(request.Host, result.Host)
	s.Equal(dependencies.SurveyManagedCapabilities{}, result.ManagedCapabilities)
	s.False(result.Executed)
	return result
}
