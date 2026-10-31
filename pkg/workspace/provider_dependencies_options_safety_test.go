package workspace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/devsy-org/devsy/internal/dependencytest"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/dependencies"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/providers"
)

func (s *providerDependencySurveySuite) TestPreparationDynamicDeferral() {
	for _, mutate := range []func(*ProviderDependencyPreparationInput){
		func(i *ProviderDependencyPreparationInput) {
			i.DynamicOptions = config.OptionDefinitions{dependencyAdapterOption: nil}
		},
		func(i *ProviderDependencyPreparationInput) {
			i.DynamicOptions = config.OptionDefinitions{dependencyAdapterOption: {Default: DefaultDockerCommand}}
		},
		func(i *ProviderDependencyPreparationInput) {
			i.StoredOptions[dependencyAdapterOption] = config.OptionValue{Children: []string{dependencyAdapterSecret}}
		},
		func(i *ProviderDependencyPreparationInput) {
			i.StoredOptions["PARENT"] = config.OptionValue{Children: []string{dependencyAdapterOption}}
		},
	} {
		for _, basis := range []ProviderDependencyBasis{ProviderDependencySaved, ProviderDependencyPreview} {
			input := s.preparationInput(DefaultDockerCommand)
			input.Basis = basis
			mutate(&input)
			if basis == ProviderDependencyPreview {
				input.ExplicitAssignments = map[string]string{
					dependencyAdapterOption: DefaultDockerCommand,
				}
			}
			dependencytest.Preserve(s.T(), s.root)
			s.preparationIssue(input, dependencyAdapterOption, "unresolved_static_input")
		}
	}
	input := s.preparationInput(DefaultDockerCommand)
	input.DynamicOptions = config.OptionDefinitions{
		providerDependencyTestUnrelated: {Command: dependencyAdapterSecret},
	}
	input.StoredOptions["PARENT"] = config.OptionValue{
		Children: []string{providerDependencyTestUnrelated},
	}
	result := s.prepare(input)
	s.Require().NotNil(result.Request)
	s.Equal(DefaultDockerCommand, result.Request.Options[dependencyAdapterOption].Value)
}

func (s *providerDependencySurveySuite) TestPreparationUntrustedManifestPolicy() {
	for _, mutate := range []func(*provider.ProviderConfig){
		func(p *provider.ProviderConfig) { p.Source = provider.ProviderSource{Raw: DefaultDockerCommand} },
		func(p *provider.ProviderConfig) { p.Source.Raw = "Docker" },
		func(p *provider.ProviderConfig) { p.Source.Raw = "lima" },
		func(p *provider.ProviderConfig) { p.Source.Raw = dependencyAdapterMicrosandbox },
		func(p *provider.ProviderConfig) { p.Source.Github = "devsy-org/devsy" },
		func(p *provider.ProviderConfig) { p.Source.File = dependencyAdapterSecret },
		func(p *provider.ProviderConfig) { p.Source.URL = "https://user:" + dependencyAdapterSecret + "@host" },
		func(p *provider.ProviderConfig) { p.Options[dependencyAdapterOption].Default = "${DYNAMIC}" },
		func(p *provider.ProviderConfig) { p.Options[dependencyAdapterOption].Cache = "1h" },
		func(p *provider.ProviderConfig) { p.Options[dependencyAdapterOption].Command = dependencyAdapterSecret },
		func(p *provider.ProviderConfig) {
			p.Options[dependencyAdapterOption].SubOptionsCommand = dependencyAdapterSecret
		},
		func(p *provider.ProviderConfig) { p.Options[dependencyAdapterOption].Required = true },
		func(p *provider.ProviderConfig) { p.Options[dependencyAdapterOption] = nil },
		func(p *provider.ProviderConfig) { p.Exec.Command[0] = dependencyAdapterSecret },
	} {
		input := s.preparationInput(DefaultDockerCommand)
		mutate(&input.Provider)
		s.preparationIssue(input, "", "dependency_policy_blocked")
	}
}

func (s *providerDependencySurveySuite) TestPreparationFutureEmbeddedDefinitionDefers() {
	original := providers.DockerProvider
	s.T().Cleanup(func() { providers.DockerProvider = original })
	for _, mutate := range []func(*provider.ProviderConfig){
		func(p *provider.ProviderConfig) { p.Options[dependencyAdapterOption].Required = true },
		func(p *provider.ProviderConfig) { p.Options[dependencyAdapterOption].Local = true },
	} {
		providers.DockerProvider = original
		input := s.preparationInput(DefaultDockerCommand)
		mutate(&input.Provider)
		encoded, err := json.Marshal(input.Provider)
		s.Require().NoError(err)
		providers.DockerProvider = string(encoded)
		s.preparationIssue(input, dependencyAdapterOption, "unresolved_static_input")
	}
}

func (s *providerDependencySurveySuite) TestPreparationInvalidEmbeddedDefinitionHasNoRequest() {
	original := providers.DockerProvider
	s.T().Cleanup(func() { providers.DockerProvider = original })
	input := s.preparationInput(DefaultDockerCommand)
	input.Provider.Options[dependencyAdapterOption].Cache = "1h"
	encoded, err := json.Marshal(input.Provider)
	s.Require().NoError(err)
	providers.DockerProvider = string(encoded)
	result, err := PrepareProviderDependencySurvey(context.Background(), input)
	s.Require().EqualError(err, "invalid embedded provider dependency contract")
	s.Equal(ProviderDependencyPreparation{}, result)
}

type providerDependencyPreparedSurveyCase struct {
	name         string
	stored       *config.OptionValue
	assignment   map[string]string
	environment  map[string]string
	origin       dependencies.LookupOrigin
	availability string
}

func (s *providerDependencySurveySuite) TestPreparationToSurvey() {
	for _, source := range []string{DefaultDockerCommand, providerDependencyPodman, providerDependencyApple} {
		binding, ok := providerDependencyBuiltIn(source)
		s.Require().True(ok)
		chosen := s.executable("chosen-" + source)
		for _, test := range providerDependencyPreparedSurveyCases(binding, chosen, s.root) {
			s.Run(source+"/"+test.name, func() {
				s.checkPreparedSurvey(binding, chosen, test)
			})
		}
	}
}

func providerDependencyPreparedSurveyCases(
	binding providerDependencyBinding,
	chosen, root string,
) []providerDependencyPreparedSurveyCase {
	return []providerDependencyPreparedSurveyCase{
		{
			name:         "reviewed-default",
			origin:       dependencies.LookupPATH,
			availability: providerDependencyTestAvailable,
		},
		{
			name:         "explicit-default",
			assignment:   map[string]string{binding.option: binding.command},
			origin:       dependencies.LookupExplicit,
			availability: providerDependencyTestAvailable,
		},
		{
			name: "env-default", environment: map[string]string{binding.option: binding.command},
			origin: dependencies.LookupExplicitEnv, availability: providerDependencyTestAvailable,
		},
		{
			name:         "stored-user-default",
			stored:       &config.OptionValue{Value: binding.command, UserProvided: true},
			origin:       dependencies.LookupExplicit,
			availability: providerDependencyTestAvailable,
		},
		{
			name: "stored-nonuser-default", stored: &config.OptionValue{Value: binding.command},
			origin: dependencies.LookupPATH, availability: providerDependencyTestAvailable,
		},
		{
			name: "stored-nonuser-chosen", stored: &config.OptionValue{Value: chosen},
			origin: dependencies.LookupExplicit, availability: providerDependencyTestAvailable,
		},
		{
			name: "explicit-chosen", assignment: map[string]string{binding.option: chosen},
			origin: dependencies.LookupExplicit, availability: providerDependencyTestAvailable,
		},
		{
			name: "missing",
			assignment: map[string]string{
				binding.option: filepath.Join(root, providerDependencyTestAbsent),
			},
			origin:       dependencies.LookupExplicit,
			availability: "missing",
		},
		{name: "explicit-empty", assignment: map[string]string{binding.option: ""}},
		{name: "env-empty", environment: map[string]string{binding.option: ""}},
		{name: "saved-user-empty", stored: &config.OptionValue{UserProvided: true}},
		{name: "saved-nonuser-empty", stored: &config.OptionValue{}},
	}
}

func (s *providerDependencySurveySuite) checkPreparedSurvey(
	binding providerDependencyBinding,
	chosen string,
	test providerDependencyPreparedSurveyCase,
) {
	input := s.preparationInput(providerDependencySourceForTest(binding))
	input.Basis = ProviderDependencyPreview
	input.ExplicitAssignments = test.assignment
	if test.stored != nil {
		input.StoredOptions[binding.option] = *test.stored
	}
	for key, value := range test.environment {
		input.Environment[strings.ToUpper(config.EnvProviderPrefix+input.Provider.Name+"_")+key] = value
	}
	result := s.prepare(input)
	s.Require().NotNil(result.Request)
	item := s.survey(*result.Request).Requirements[0]
	if test.availability == "" {
		s.Equal(dependencies.LookupInvalidPath, item.ReasonCode)
		s.Empty(item.Candidates)
		return
	}
	s.Equal(test.availability, item.Availability)
	s.Require().Len(item.Candidates, 1)
	s.Equal(test.origin, item.Candidates[0].Origin)
	if strings.Contains(test.name, "chosen") {
		s.Equal(chosen, item.Candidates[0].LexicalPath)
	}
}

func (s *providerDependencySurveySuite) TestPreparationEndpointAndKubernetesIsolation() {
	var contacted atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		contacted.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	endpoint := strings.Replace(server.URL, "://", "://user:"+dependencyAdapterSecret+"@", 1)
	for _, source := range []string{
		DefaultDockerCommand, providerDependencyPodman, providerDependencyApple, providerDependencyKubernetes,
	} {
		input := s.preparationInput(source)
		input.Basis = ProviderDependencyPreview
		input.ExplicitAssignments = map[string]string{}
		for _, name := range []string{
			providerDependencyTestDockerHost, "DOCKER_CONTEXT", "DOCKER_TLS_VERIFY", "DOCKER_BUILDER",
			"PODMAN_HOST", "PODMAN_ELEVATION", "ROSETTA", "KUBECONFIG", "KUBERNETES_CONTEXT",
		} {
			input.ExplicitAssignments[name] = endpoint
			input.StoredOptions[name] = config.OptionValue{Value: endpoint, UserProvided: true}
			input.Environment[name] = endpoint
		}
		result := s.prepare(input)
		s.Require().NotNil(result.Request)
		encoded, err := json.Marshal(
			[]any{result.Request.Options, result.Provenance, result.Issues},
		)
		s.Require().NoError(err)
		s.NotContains(string(encoded), dependencyAdapterSecret)
		observed := s.survey(*result.Request)
		if source == providerDependencyKubernetes {
			s.Equal(config.OptionValue{}, result.Request.Options["KUBERNETES_CONFIG"])
			s.Empty(observed.Requirements[0].Candidates)
			input.ExplicitAssignments["KUBERNETES_CONFIG"] = filepath.Join(s.root, "exec-auth")
			withConfig := s.prepare(input)
			s.Require().NotNil(withConfig.Request)
			s.Equal(observed, s.survey(*withConfig.Request))
			continue
		}
		binding, ok := providerDependencyBuiltIn(source)
		s.Require().True(ok)
		input.ExplicitAssignments[binding.option] = endpoint
		result = s.prepare(input)
		s.Require().NotNil(result.Request)
		observed = s.survey(*result.Request)
		s.Equal(dependencies.LookupInvalidPath, observed.Requirements[0].ReasonCode)
		s.Empty(observed.Requirements[0].Candidates)
		encoded, err = json.Marshal(observed)
		s.Require().NoError(err)
		s.NotContains(string(encoded), dependencyAdapterSecret)
	}
	s.Zero(contacted.Load())
}

func (s *providerDependencySurveySuite) TestPreparationCanceledHasNoPartialResult() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, invalid := range []bool{false, true} {
		input := s.preparationInput(DefaultDockerCommand)
		if invalid {
			input.Basis = ""
		}
		dependencytest.Preserve(s.T(), s.root)
		result, err := PrepareProviderDependencySurvey(ctx, input)
		s.ErrorIs(err, context.Canceled)
		s.Equal(ProviderDependencyPreparation{}, result)
	}
}

type providerDependencyCancelOnEntry struct {
	context.Context
	cancel context.CancelFunc
}

func (c providerDependencyCancelOnEntry) Err() error {
	err := c.Context.Err()
	c.cancel()
	return err
}

func (s *providerDependencySurveySuite) TestPreparationCancellationDiscardsCompletedAssembly() {
	for _, deferred := range []bool{false, true} {
		input := s.preparationInput(DefaultDockerCommand)
		if deferred {
			input.DynamicOptions = config.OptionDefinitions{dependencyAdapterOption: nil}
		}
		ctx, cancel := context.WithCancel(context.Background())
		s.T().Cleanup(cancel)
		result, err := PrepareProviderDependencySurvey(
			providerDependencyCancelOnEntry{ctx, cancel},
			input,
		)
		s.ErrorIs(err, context.Canceled)
		s.Equal(ProviderDependencyPreparation{}, result)
	}
}

func (s *providerDependencySurveySuite) TestPreparationOnlySuppliedEnvironment() {
	input := s.preparationInput(DefaultDockerCommand)
	input.Basis = ProviderDependencyPreview
	key := strings.ToUpper(
		config.EnvProviderPrefix + input.Provider.Name + "_" + dependencyAdapterOption,
	)
	s.T().Setenv(key, dependencyAdapterSecret)
	s.T().Setenv(dependencyAdapterOption, dependencyAdapterSecret)
	delete(input.Environment, "PATH")
	dependencytest.Preserve(s.T(), s.root)
	result := s.prepare(input)
	s.Require().NotNil(result.Request)
	s.Equal(
		config.OptionValue{Value: DefaultDockerCommand},
		result.Request.Options[dependencyAdapterOption],
	)
	item := s.survey(*result.Request).Requirements[0]
	s.Equal("missing", item.Availability)
	s.Empty(item.Candidates)
}
