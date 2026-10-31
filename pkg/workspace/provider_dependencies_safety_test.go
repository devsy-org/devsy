package workspace

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/devsy-org/devsy/internal/dependencytest"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/dependencies"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/devsy-org/devsy/providers"
	"github.com/stretchr/testify/require"
)

func (s *providerDependencySurveySuite) TestUnreviewedProvenanceIsBlocked() {
	for _, source := range []provider.ProviderSource{
		{},
		{Raw: DefaultDockerCommand},
		{Internal: true, Raw: "Docker"},
		{Internal: true, Raw: " docker"},
		{Internal: true, Raw: providerDependencyKubernetes},
		{Internal: true, Raw: dependencyAdapterMicrosandbox},
		{Internal: true, Raw: "lima"},
		{Internal: true, Raw: "orbstack"},
		{Internal: true, Raw: DefaultDockerCommand, Github: "devsy-org/devsy"},
		{Internal: true, Raw: DefaultDockerCommand, File: "provider.yaml"},
		{Internal: true, Raw: DefaultDockerCommand, URL: "https://github.com/devsy-org/devsy"},
		{Github: dependencyAdapterMicrosandboxRepo, Raw: DefaultDockerCommand},
		{Github: "devsy-org/devsy-provider-lima", Raw: DefaultDockerCommand},
		{Github: "devsy-org/devsy-provider-orbstack", Raw: DefaultDockerCommand},
		{File: "provider.yaml", Raw: DefaultDockerCommand},
		{URL: "https://user:" + dependencyAdapterSecret + "@github.com/devsy-org/devsy", Raw: DefaultDockerCommand},
	} {
		request := s.request(DefaultDockerCommand)
		request.Provider.Source = source
		s.blocked(request)
	}
	for _, source := range []string{dependencyAdapterMicrosandbox, "pro"} {
		s.blocked(s.request(source))
	}
}

func (s *providerDependencySurveySuite) TestEveryManifestSectionIsComparedWithoutExecution() {
	path := filepath.Join(s.bin, "exec-auth")
	command := path + " " + dependencyAdapterSecret
	for _, mutate := range []func(*provider.ProviderConfig){
		func(p *provider.ProviderConfig) { p.Exec.Command = types.StrArray{command} },
		func(p *provider.ProviderConfig) { p.Exec.Init = types.StrArray{command} },
		func(p *provider.ProviderConfig) { p.Agent.Exec.Shutdown = types.StrArray{command} },
		func(p *provider.ProviderConfig) { p.Agent.Driver = provider.CustomDriver },
		func(p *provider.ProviderConfig) { p.Agent.Docker.Path = command },
		func(p *provider.ProviderConfig) { p.Agent.Docker.Env["DOCKER_HOST"] = command },
		func(p *provider.ProviderConfig) { p.Agent.Docker.Elevation = dependencyAdapterElevation },
		func(p *provider.ProviderConfig) { p.Options[dependencyAdapterOption].Command = command },
		func(p *provider.ProviderConfig) { p.Options[dependencyAdapterOption].SubOptionsCommand = command },
		func(p *provider.ProviderConfig) { p.Options[dependencyAdapterOption].Default = command },
		func(p *provider.ProviderConfig) { p.Options["INACTIVITY_TIMEOUT"].Command = command },
		func(p *provider.ProviderConfig) { p.Options[dependencyAdapterOption] = nil },
		func(p *provider.ProviderConfig) { p.Binaries = dependencyAdapterBinaries(path) },
		func(p *provider.ProviderConfig) { p.Agent.Binaries = dependencyAdapterBinaries(path) },
		func(p *provider.ProviderConfig) { p.Description = command },
		func(p *provider.ProviderConfig) { p.OptionGroups[0].Options = []string{command} },
		func(p *provider.ProviderConfig) { p.Version = "v999.0.0" },
	} {
		request := s.request(DefaultDockerCommand)
		mutate(&request.Provider)
		s.blocked(request)
	}
}

func dependencyAdapterBinaries(path string) map[string][]*provider.ProviderBinary {
	return map[string][]*provider.ProviderBinary{
		"ADAPTER": {
			{Path: path, OS: "linux", Arch: "amd64", Checksum: "must-not-verify-or-remove"},
		},
	}
}

func (s *providerDependencySurveySuite) TestEmbeddedDriverMappingDriftIsBlocked() {
	for _, test := range []struct {
		source   string
		embedded *string
		mutate   func(*provider.ProviderConfig)
	}{
		{DefaultDockerCommand, &providers.DockerProvider, func(p *provider.ProviderConfig) {
			p.Options[dependencyAdapterOption].Default = "different-docker"
		}},
		{DefaultDockerCommand, &providers.DockerProvider, func(p *provider.ProviderConfig) {
			p.Agent.Docker.Path = "${UNREVIEWED_PATH}"
		}},
		{providerDependencyPodman, &providers.PodmanProvider, func(p *provider.ProviderConfig) {
			p.Agent.Docker.Runtime = DefaultDockerCommand
		}},
		{providerDependencyApple, &providers.AppleProvider, func(p *provider.ProviderConfig) {
			p.Agent.Apple.Path = "${UNREVIEWED_PATH}"
		}},
		{providerDependencyKubernetes, &providers.KubernetesProvider, func(p *provider.ProviderConfig) {
			p.Agent.Kubernetes.KubernetesConfig = "${UNREVIEWED_CONFIG}"
		}},
	} {
		request := s.request(test.source)
		test.mutate(&request.Provider)
		encoded, err := json.Marshal(request.Provider)
		s.Require().NoError(err)
		original := *test.embedded
		s.T().Cleanup(func() { *test.embedded = original })
		*test.embedded = string(encoded)
		s.blocked(request)
		*test.embedded = original
	}
}

func (s *providerDependencySurveySuite) TestExplicitPathsUseSnapshotCWDAndNeverFallback() {
	request := s.request(DefaultDockerCommand)
	request.Options[dependencyAdapterOption] = config.OptionValue{
		Value: filepath.Join("tools", DefaultDockerCommand), UserProvided: true,
	}
	item := s.survey(request).Requirements[0]
	s.Equal("available", item.Availability)
	s.Require().Len(item.Candidates, 1)
	s.Equal(dependencies.LookupExplicit, item.Candidates[0].Origin)
	s.True(filepath.IsAbs(item.Candidates[0].LexicalPath))
	request.Options[dependencyAdapterOption] = config.OptionValue{
		Value: filepath.Join(s.root, "missing-docker"), UserProvided: true,
	}
	item = s.survey(request).Requirements[0]
	s.Equal("missing", item.Availability)
	for _, candidate := range item.Candidates {
		s.Equal(dependencies.FileMissing, candidate.State)
		s.NotEqual(filepath.Join(s.bin, DefaultDockerCommand), candidate.LexicalPath)
	}
}

func (s *providerDependencySurveySuite) TestKubernetesDoesNotReadConfigOrInvokeExecAuth() {
	request := s.request(providerDependencyKubernetes)
	configPath := filepath.Join(s.root, "kubeconfig")
	body := "apiVersion: v1\nusers:\n- name: private\n  user:\n    exec:\n      command: " +
		filepath.Join(s.bin, "exec-auth") + "\n      args: [" + dependencyAdapterSecret + "]\n"
	s.Require().NoError(os.WriteFile(configPath, []byte(body), 0o600))
	request.Options["KUBERNETES_CONFIG"] = config.OptionValue{Value: configPath, UserProvided: true}
	request.Environment["KUBECONFIG"] = configPath
	result := s.survey(request)
	s.True(result.ContractVerified)
	s.Require().Len(result.Requirements, 1)
	item := result.Requirements[0]
	s.Equal("KUBERNETES_CONFIG", item.Requirement.ConfigurationOption)
	s.Empty(item.Requirement.ExecutableOption)
	s.Empty(item.Candidates)
	s.Equal("unknown", item.Availability)
	s.Equal("not_checked", item.Reachability)
	request.Options["KUBERNETES_CONFIG"] = config.OptionValue{
		Value: filepath.Join(s.root, "absent"),
	}
	s.Equal(result, s.survey(request), "survey must not depend on kubeconfig contents or presence")
}

func (s *providerDependencySurveySuite) TestErrorsAndURIOptionsDoNotEchoInput() {
	for _, mutate := range []func(*ProviderDependencySurveyRequest){
		func(r *ProviderDependencySurveyRequest) { r.WorkingDirectory = dependencyAdapterSecret },
		func(r *ProviderDependencySurveyRequest) { r.Host.GOOS = dependencyAdapterSecret },
		func(r *ProviderDependencySurveyRequest) { r.Host.GOARCH = dependencyAdapterSecret },
		func(r *ProviderDependencySurveyRequest) {
			r.Options[dependencyAdapterOption] = config.OptionValue{Value: DefaultDockerCommand}
			r.OptionOrigins[dependencyAdapterOption] = dependencies.LookupOrigin(dependencyAdapterSecret)
		},
	} {
		request := s.request(DefaultDockerCommand)
		mutate(&request)
		dependencytest.Preserve(s.T(), s.root)
		result, err := SurveyProviderDependencies(context.Background(), request)
		s.Require().Error(err)
		s.NotContains(err.Error(), dependencyAdapterSecret)
		s.Equal(dependencies.SurveyResult{}, result)
	}
	request := s.request(DefaultDockerCommand)
	request.Options[dependencyAdapterOption] = config.OptionValue{
		Value: "https://user:" + dependencyAdapterSecret + "@host/docker", UserProvided: true,
	}
	result := s.survey(request)
	s.Equal(dependencies.LookupInvalidPath, result.Requirements[0].ReasonCode)
	s.Empty(result.Requirements[0].Candidates)
	encoded, err := json.Marshal(result)
	s.Require().NoError(err)
	s.NotContains(string(encoded), dependencyAdapterSecret)
}

func (s *providerDependencySurveySuite) TestCanceledSurveyHasNoPartialResult() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dependencytest.Preserve(s.T(), s.root)
	result, err := SurveyProviderDependencies(ctx, s.request(DefaultDockerCommand))
	s.ErrorIs(err, context.Canceled)
	s.Equal(dependencies.SurveyResult{}, result)
}

func (s *providerDependencySurveySuite) TestConfiguredEndpointIsNotContacted() {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	endpoint := strings.Replace(server.URL, "://", "://user:"+dependencyAdapterSecret+"@", 1)
	for _, source := range []string{DefaultDockerCommand, providerDependencyPodman} {
		request := s.request(source)
		request.Environment["DOCKER_HOST"] = endpoint
		request.Options["DOCKER_HOST"] = config.OptionValue{Value: endpoint, UserProvided: true}
		request.Options["PODMAN_HOST"] = config.OptionValue{Value: endpoint, UserProvided: true}
		result := s.survey(request)
		encoded, err := json.Marshal(result)
		s.Require().NoError(err)
		s.NotContains(string(encoded), dependencyAdapterSecret)
	}
	s.Zero(requests.Load())
}

func TestProviderDependencyAdapterEffectfulEntryPointsAreAbsent(t *testing.T) {
	files, err := filepath.Glob("provider_dependencies*.go")
	require.NoError(t, err)
	require.NotEmpty(t, files)
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		require.NoError(t, err)
		for _, imported := range file.Imports {
			name, err := strconv.Unquote(imported.Path.Value)
			require.NoError(t, err)
			require.Contains(t, []string{
				"bytes",
				"context",
				"encoding/json",
				"errors",
				"strings",
				"github.com/devsy-org/devsy/pkg/config",
				"github.com/devsy-org/devsy/pkg/dependencies",
				"github.com/devsy-org/devsy/pkg/provider",
				"github.com/devsy-org/devsy/pkg/types",
				"github.com/devsy-org/devsy/providers",
			}, name)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			providerDependencyAllowedCall(t, call, path)
			return true
		})
	}
}

func providerDependencyAllowedCall(t *testing.T, call *ast.CallExpr, path string) {
	t.Helper()
	var name string
	if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
		name = selector.Sel.Name
	} else if ident, ok := call.Fun.(*ast.Ident); ok {
		name = ident.Name
	}
	require.NotContains(t, []string{
		"LoadProviderConfig",
		"ResolveProvider",
		"ResolveOptions",
		"GetBinaries",
		"GetBaseEnvironment",
		"ToOptions",
		"ToEnvironment",
		"GetProviderDir",
		"GetProviderBinariesDir",
		"EnsureElevated",
		"NewDriver",
		"GetDriver",
		"GetWorkspaceDriver",
		"verifyOrRemoveBinary",
		"Environ",
		"Getenv",
		"LookupEnv",
		"Setenv",
		"Chdir",
	}, name, path)
}

func (s *providerDependencySurveySuite) blocked(request ProviderDependencySurveyRequest) {
	result := s.survey(request)
	s.False(result.ContractVerified)
	s.Equal("dependency_policy_blocked", result.PolicyReason)
	s.Empty(result.Requirements)
	encoded, err := json.Marshal(result)
	s.Require().NoError(err)
	s.NotContains(string(encoded), dependencyAdapterSecret)
}
