package workspace

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/internal/dependencytest"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/dependencies"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/stretchr/testify/require"
)

const (
	providerDependencyTestValue      = "value"
	providerDependencyTestAbsent     = "absent"
	providerDependencyTestEmpty      = "empty"
	providerDependencyTestAvailable  = "available"
	providerDependencyTestUnrelated  = "UNRELATED"
	providerDependencyTestChanged    = "changed"
	providerDependencyTestDockerHost = "DOCKER_HOST"
)

func (s *providerDependencySurveySuite) preparationInput(
	source string,
) ProviderDependencyPreparationInput {
	request := s.request(source)
	return ProviderDependencyPreparationInput{
		Basis: ProviderDependencySaved, Provider: request.Provider,
		StoredOptions: request.Options, Environment: request.Environment,
		WorkingDirectory: request.WorkingDirectory, Target: request.Target, Host: request.Host,
	}
}

func (s *providerDependencySurveySuite) prepare(
	input ProviderDependencyPreparationInput,
) ProviderDependencyPreparation {
	dependencytest.Preserve(s.T(), s.root)
	before, err := json.Marshal(input)
	s.Require().NoError(err)
	result, err := PrepareProviderDependencySurvey(context.Background(), input)
	s.Require().NoError(err)
	after, err := json.Marshal(input)
	s.Require().NoError(err)
	s.Equal(string(before), string(after), "preparation mutated its input")
	if input.Basis == ProviderDependencySaved || input.Basis == ProviderDependencyPreview {
		s.Equal(input.Basis, result.Basis)
	} else {
		s.Empty(result.Basis)
	}
	return result
}

func (s *providerDependencySurveySuite) TestPreparationPrecedenceAndPresence() {
	presence := []string{
		providerDependencyTestAbsent,
		providerDependencyTestEmpty,
		providerDependencyTestValue,
	}
	for _, source := range []string{
		DefaultDockerCommand, providerDependencyPodman, providerDependencyApple, providerDependencyKubernetes,
	} {
		binding, ok := providerDependencyBuiltIn(source)
		s.Require().True(ok)
		for _, basis := range []ProviderDependencyBasis{ProviderDependencySaved, ProviderDependencyPreview} {
			for _, stored := range []string{providerDependencyTestAbsent, "user", "nonuser", "user-empty", "nonuser-empty"} {
				for _, assignment := range presence {
					for _, env := range presence {
						s.Run(source+"/"+string(basis)+"/"+stored+"/"+assignment+"/"+env, func() {
							s.checkPreparationPrecedence(
								binding,
								basis,
								providerDependencyPrecedenceCase{stored, assignment, env},
							)
						})
					}
				}
			}
		}
	}
}

type providerDependencyPrecedenceCase struct {
	stored, assignment, environment string
}

func (s *providerDependencySurveySuite) checkPreparationPrecedence(
	binding providerDependencyBinding,
	basis ProviderDependencyBasis,
	test providerDependencyPrecedenceCase,
) {
	stored, assignment, env := test.stored, test.assignment, test.environment
	input := s.preparationInput(providerDependencySourceForTest(binding))
	input.Basis = basis
	want := config.OptionValue{Value: binding.command}
	provenance := ProviderDependencyDefault
	if stored != providerDependencyTestAbsent {
		want = providerDependencyStoredTestValue(stored)
		input.StoredOptions[binding.option] = want
		provenance = ProviderDependencySavedNonUser
		if want.UserProvided {
			provenance = ProviderDependencySavedUser
		}
	}
	if env != providerDependencyTestAbsent {
		value := providerDependencyPresenceValue(env, "env-literal")
		input.Environment[strings.ToUpper(config.EnvProviderPrefix+input.Provider.Name+"_")+binding.option] = value
		if basis == ProviderDependencyPreview {
			want = config.OptionValue{
				Value:        value,
				UserProvided: true,
				Children:     input.StoredOptions[binding.option].Children,
			}
			provenance = ProviderDependencyEnvironment
		}
	}
	if assignment != providerDependencyTestAbsent {
		value := providerDependencyPresenceValue(assignment, "explicit-literal")
		input.ExplicitAssignments = map[string]string{binding.option: value}
		if basis == ProviderDependencySaved {
			s.preparationIssue(input, "", "preview_assignments_required")
			return
		}
		want = config.OptionValue{
			Value:        value,
			UserProvided: true,
			Children:     input.StoredOptions[binding.option].Children,
		}
		provenance = ProviderDependencyExplicit
	}
	result := s.prepare(input)
	s.Require().NotNil(result.Request)
	s.Empty(result.Issues)
	s.Equal(map[string]config.OptionValue{binding.option: want}, result.Request.Options)
	s.Equal(
		map[string]ProviderDependencyOptionProvenance{binding.option: provenance},
		result.Provenance,
	)
	wantOrigin := providerDependencyExpectedTestOrigin(provenance)
	s.Equal(
		map[string]dependencies.LookupOrigin{binding.option: wantOrigin},
		result.Request.OptionOrigins,
	)
}

func providerDependencyStoredTestValue(stored string) config.OptionValue {
	value := config.OptionValue{
		Value: "stored-literal", UserProvided: strings.HasPrefix(stored, "user"),
		Filled: &[]types.Time{types.Unix(123, 0)}[0], Children: []string{},
	}
	if strings.HasSuffix(stored, providerDependencyTestEmpty) {
		value.Value = ""
	}
	return value
}

func providerDependencyPresenceValue(presence, nonempty string) string {
	if presence == providerDependencyTestValue {
		return nonempty
	}
	return ""
}

func providerDependencyExpectedTestOrigin(
	provenance ProviderDependencyOptionProvenance,
) dependencies.LookupOrigin {
	switch provenance {
	case ProviderDependencyExplicit:
		return dependencies.LookupExplicit
	case ProviderDependencyEnvironment:
		return dependencies.LookupExplicitEnv
	default:
		return dependencies.SurveyDefault
	}
}

func providerDependencySourceForTest(binding providerDependencyBinding) string {
	if binding.configuration {
		return providerDependencyKubernetes
	}
	if binding.tool == "apple-container" {
		return providerDependencyApple
	}
	return binding.tool
}

func (s *providerDependencySurveySuite) preparationIssue(
	input ProviderDependencyPreparationInput,
	option, reason string,
) {
	result := s.prepare(input)
	s.Nil(result.Request)
	s.Empty(result.Provenance)
	s.Equal([]ProviderDependencyPreparationIssue{{Option: option, Reason: reason}}, result.Issues)
	encoded, err := json.Marshal(result.Issues)
	s.Require().NoError(err)
	s.NotContains(string(encoded), dependencyAdapterSecret)
}

func (s *providerDependencySurveySuite) TestPreparationInstallationPrefix() {
	for _, source := range []string{
		DefaultDockerCommand, providerDependencyPodman, providerDependencyApple, providerDependencyKubernetes,
	} {
		input := s.preparationInput(source)
		binding, ok := providerDependencyBuiltIn(source)
		s.Require().True(ok)
		input.Basis = ProviderDependencyPreview
		input.Provider.Name = "my-" + source
		prefix := strings.ToUpper(config.EnvProviderPrefix + input.Provider.Name + "_")
		input.Environment[binding.option] = dependencyAdapterSecret
		input.Environment[strings.ReplaceAll(prefix, "-", "_")+binding.option] = dependencyAdapterSecret
		input.Environment[strings.ToUpper(config.EnvProviderPrefix+source+"_")+binding.option] = dependencyAdapterSecret
		result := s.prepare(input)
		s.Require().NotNil(result.Request)
		s.Equal(binding.command, result.Request.Options[binding.option].Value)
		input.Environment[prefix+binding.option] = "selected-literal"
		result = s.prepare(input)
		s.Require().NotNil(result.Request)
		s.Equal("selected-literal", result.Request.Options[binding.option].Value)
		s.Equal(ProviderDependencyEnvironment, result.Provenance[binding.option])
	}
}

func (s *providerDependencySurveySuite) TestPreparationCanonicalAssignmentsAndBasis() {
	for _, basis := range []ProviderDependencyBasis{"", "unknown", dependencyAdapterSecret} {
		input := s.preparationInput(DefaultDockerCommand)
		input.Basis = basis
		s.preparationIssue(input, "", "invalid_basis")
	}
	for _, key := range []string{"", "docker_path", " DOCKER_PATH", "DOCKER_PATH ", "DOCKER_PATH=x", "X\x00"} {
		input := s.preparationInput(DefaultDockerCommand)
		input.Basis = ProviderDependencyPreview
		input.ExplicitAssignments = map[string]string{key: dependencyAdapterSecret}
		s.preparationIssue(input, "", "invalid_canonical_assignment")
	}
	input := s.preparationInput(DefaultDockerCommand)
	input.Basis = ProviderDependencyPreview
	input.ExplicitAssignments = map[string]string{
		providerDependencyTestDockerHost: dependencyAdapterSecret,
		providerDependencyTestUnrelated:  "",
	}
	result := s.prepare(input)
	s.Require().NotNil(result.Request)
	s.Equal(
		map[string]config.OptionValue{dependencyAdapterOption: {Value: DefaultDockerCommand}},
		result.Request.Options,
	)
	s.Equal(
		map[string]ProviderDependencyOptionProvenance{
			dependencyAdapterOption: ProviderDependencyDefault,
		},
		result.Provenance,
	)
}

func (s *providerDependencySurveySuite) TestPreparationStoredHistoryAndLiteralValues() {
	for _, basis := range []ProviderDependencyBasis{ProviderDependencySaved, ProviderDependencyPreview} {
		for _, value := range []string{DefaultDockerCommand, "$HOME/${DOCKER_PATH}", "~/docker", "${X:-fallback}"} {
			input := s.preparationInput(DefaultDockerCommand)
			input.Basis = basis
			input.StoredOptions[dependencyAdapterOption] = config.OptionValue{
				Value:        value,
				UserProvided: true,
			}
			input.Environment[dependencyAdapterOption] = value
			if basis == ProviderDependencySaved {
				key := strings.ToUpper(
					config.EnvProviderPrefix + input.Provider.Name + "_" + dependencyAdapterOption,
				)
				input.Environment[key] = value
			}
			result := s.prepare(input)
			s.Require().NotNil(result.Request)
			s.Equal(value, result.Request.Options[dependencyAdapterOption].Value)
			s.Equal(ProviderDependencySavedUser, result.Provenance[dependencyAdapterOption])
			s.Equal(
				dependencies.SurveyDefault,
				result.Request.OptionOrigins[dependencyAdapterOption],
			)
			input.Basis = ProviderDependencyPreview
			input.ExplicitAssignments = map[string]string{dependencyAdapterOption: value}
			result = s.prepare(input)
			s.Require().NotNil(result.Request)
			s.Equal(value, result.Request.Options[dependencyAdapterOption].Value)
		}
	}
}

func (s *providerDependencySurveySuite) TestPreparationEnvironmentSnapshot() {
	input := s.preparationInput(DefaultDockerCommand)
	input.Basis = ProviderDependencyPreview
	key := strings.ToUpper(
		config.EnvProviderPrefix + input.Provider.Name + "_" + dependencyAdapterOption,
	)
	input.Environment[strings.ToLower(key)] = "lower-case"
	result := s.prepare(input)
	s.Require().NotNil(result.Request)
	want := DefaultDockerCommand
	if runtime.GOOS == execTestWindowsOS {
		want = "lower-case"
	}
	s.Equal(want, result.Request.Options[dependencyAdapterOption].Value)
	input.Environment[key] = dependencyAdapterSecret
	if runtime.GOOS == execTestWindowsOS {
		s.preparationIssue(input, "", "invalid_invocation_environment")
		input.Environment[strings.ToLower(key)] = dependencyAdapterSecret
		s.preparationIssue(input, "", "invalid_invocation_environment")
	} else {
		result = s.prepare(input)
		s.Require().NotNil(result.Request)
		s.Equal(dependencyAdapterSecret, result.Request.Options[dependencyAdapterOption].Value)
		input.Host.GOOS = execTestWindowsOS
		delete(input.Environment, key)
		result = s.prepare(input)
		s.Require().NotNil(result.Request)
		s.Equal(DefaultDockerCommand, result.Request.Options[dependencyAdapterOption].Value)
	}
	input.WorkingDirectory = "relative-" + dependencyAdapterSecret
	s.preparationIssue(input, "", "invalid_invocation_environment")
}

func TestProviderDependencyStaticDefinitionsFailClosed(t *testing.T) {
	binding, ok := providerDependencyBuiltIn(DefaultDockerCommand)
	require.True(t, ok)
	for _, mutate := range []func(*types.Option){
		func(o *types.Option) { o.Default = "${UNRESOLVED}" },
		func(o *types.Option) { o.Command = dependencyAdapterSecret },
		func(o *types.Option) { o.SubOptionsCommand = dependencyAdapterSecret },
		func(o *types.Option) { o.Cache = "1h" },
		func(o *types.Option) { o.Required = true },
		func(o *types.Option) { o.Local = true },
		func(o *types.Option) { o.Global = true },
		func(o *types.Option) { o.Type = "boolean" },
		func(o *types.Option) { o.Enum = types.OptionEnumArray{{Value: "choice"}} },
		func(o *types.Option) { o.ValidationPattern = dependencyAdapterSecret },
	} {
		option := &types.Option{Default: DefaultDockerCommand}
		require.True(t, providerDependencyStaticOption(option, binding))
		mutate(option)
		require.False(t, providerDependencyStaticOption(option, binding))
	}
	require.False(t, providerDependencyStaticOption(nil, binding))
}

func (s *providerDependencySurveySuite) TestPreparationInputAndReturnIsolation() {
	input := s.preparationInput(DefaultDockerCommand)
	filled := types.Unix(123, 0)
	input.StoredOptions[dependencyAdapterOption] = config.OptionValue{
		Value: DefaultDockerCommand, Filled: &filled, Children: []string{},
	}
	input.DynamicOptions = config.OptionDefinitions{
		providerDependencyTestUnrelated: {Command: dependencyAdapterSecret},
	}
	dependencytest.Preserve(s.T(), s.root)
	result := s.prepare(input)
	s.Require().NotNil(result.Request)
	before, err := json.Marshal(result)
	s.Require().NoError(err)
	input.Provider.Options[dependencyAdapterOption].Default = dependencyAdapterSecret
	input.Provider.Agent.Docker.Env[providerDependencyTestDockerHost] = dependencyAdapterSecret
	input.Provider.OptionGroups[0].Options[0] = dependencyAdapterSecret
	input.Provider.Exec.Command[0] = dependencyAdapterSecret
	input.Environment["PATH"] = dependencyAdapterSecret
	input.StoredOptions[dependencyAdapterOption] = config.OptionValue{
		Value: dependencyAdapterSecret,
	}
	input.DynamicOptions[providerDependencyTestUnrelated].Command = providerDependencyTestChanged
	filled.Time = types.Unix(456, 0).Time
	input.Target.ProviderFingerprint = providerDependencyTestChanged
	input.Host.GOOS = providerDependencyTestChanged
	input.WorkingDirectory = providerDependencyTestChanged
	after, err := json.Marshal(result)
	s.Require().NoError(err)
	s.Equal(string(before), string(after))
	s.NotSame(&filled, result.Request.Options[dependencyAdapterOption].Filled)
	observation := s.survey(*result.Request)
	s.True(observation.ContractVerified)
	s.Equal(providerDependencyTestAvailable, observation.Requirements[0].Availability)
}
