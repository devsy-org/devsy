package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/dependencies"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
)

type ProviderDependencyBasis string

const (
	ProviderDependencySaved   ProviderDependencyBasis = "saved_provider"
	ProviderDependencyPreview ProviderDependencyBasis = "configuration_preview"
)

type ProviderDependencyOptionProvenance string

const (
	ProviderDependencyDefault      ProviderDependencyOptionProvenance = "default"
	ProviderDependencyExplicit     ProviderDependencyOptionProvenance = "explicit_option"
	ProviderDependencyEnvironment  ProviderDependencyOptionProvenance = "explicit_env"
	ProviderDependencySavedUser    ProviderDependencyOptionProvenance = "saved_user"
	ProviderDependencySavedNonUser ProviderDependencyOptionProvenance = "saved_nonuser"
)

// ProviderDependencyPreparationInput is an internal, provider-level snapshot.
// Callers own its data and must not mutate it during preparation.
type ProviderDependencyPreparationInput struct {
	Basis               ProviderDependencyBasis
	Provider            provider.ProviderConfig
	StoredOptions       map[string]config.OptionValue
	DynamicOptions      config.OptionDefinitions
	ExplicitAssignments map[string]string
	Environment         map[string]string
	WorkingDirectory    string
	Target              dependencies.SurveyTarget
	Host                dependencies.SurveyHost
}

type ProviderDependencyPreparationIssue struct {
	Option string
	Reason string
}

// ProviderDependencyPreparation contains private invocation data, not CLI output.
// Request is nil on every incomplete or policy-blocked preparation.
type ProviderDependencyPreparation struct {
	Basis      ProviderDependencyBasis
	Request    *ProviderDependencySurveyRequest
	Provenance map[string]ProviderDependencyOptionProvenance
	Issues     []ProviderDependencyPreparationIssue
}

func PrepareProviderDependencySurvey(
	ctx context.Context,
	input ProviderDependencyPreparationInput,
) (ProviderDependencyPreparation, error) {
	if err := ctx.Err(); err != nil {
		return ProviderDependencyPreparation{}, err
	}
	result, err := prepareProviderDependencySurvey(input)
	if canceled := ctx.Err(); canceled != nil {
		return ProviderDependencyPreparation{}, canceled
	}
	return result, err
}

func prepareProviderDependencySurvey(
	input ProviderDependencyPreparationInput,
) (ProviderDependencyPreparation, error) {
	if reason := providerDependencyInputIssue(input); reason != "" {
		return providerDependencyPreparationIssue(input.Basis, "", reason), nil
	}
	manifest, err := copyProviderDependencyManifest(input.Provider)
	if err != nil {
		return ProviderDependencyPreparation{}, err
	}
	binding, trusted, err := providerDependencyPreparationBinding(manifest)
	if err != nil {
		return ProviderDependencyPreparation{}, err
	}
	if !trusted {
		return providerDependencyPreparationIssue(input.Basis, "", "dependency_policy_blocked"), nil
	}
	if !providerDependencyStaticOption(manifest.Options[binding.option], binding) ||
		providerDependencyDynamicInput(input, binding.option) {
		return providerDependencyPreparationIssue(
			input.Basis,
			binding.option,
			"unresolved_static_input",
		), nil
	}
	return prepareProviderDependencyInvocation(input, manifest, binding), nil
}

func providerDependencyInputIssue(input ProviderDependencyPreparationInput) string {
	if input.Basis != ProviderDependencySaved && input.Basis != ProviderDependencyPreview {
		return "invalid_basis"
	}
	if input.Basis == ProviderDependencySaved && len(input.ExplicitAssignments) != 0 {
		return "preview_assignments_required"
	}
	if !providerDependencyCanonicalAssignments(input.ExplicitAssignments) {
		return "invalid_canonical_assignment"
	}
	return ""
}

func providerDependencyCanonicalAssignments(assignments map[string]string) bool {
	for key := range assignments {
		if key == "" || key != strings.TrimSpace(strings.ToUpper(key)) ||
			strings.ContainsAny(key, "=\x00") {
			return false
		}
	}
	return true
}

func copyProviderDependencyManifest(
	manifest provider.ProviderConfig,
) (provider.ProviderConfig, error) {
	// The typed round trip isolates every nested manifest field before establishing trust.
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return provider.ProviderConfig{}, errors.New("cannot copy provider dependency manifest")
	}
	var copied provider.ProviderConfig
	if err := json.Unmarshal(encoded, &copied); err != nil {
		return provider.ProviderConfig{}, errors.New("cannot copy provider dependency manifest")
	}
	return copied, nil
}

func providerDependencyPreparationBinding(
	manifest provider.ProviderConfig,
) (providerDependencyBinding, bool, error) {
	catalog, err := dependencies.LoadCatalog()
	if err != nil {
		return providerDependencyBinding{}, false, errors.New(
			"cannot load provider dependency catalog",
		)
	}
	contract, err := providerDependencyContract(catalog, manifest)
	if err != nil {
		return providerDependencyBinding{}, false, err
	}
	if contract.Source.Kind != dependencies.SourceInternal {
		return providerDependencyBinding{}, false, nil
	}
	binding, recognized := providerDependencyBuiltIn(contract.Source.InternalID)
	return binding, recognized, nil
}

func providerDependencyStaticOption(option *types.Option, binding providerDependencyBinding) bool {
	// Even future embedded definitions must not silently turn dynamic work into a default.
	return option != nil && binding.matchesOption(option) && providerDependencyStaticShape(option)
}

func providerDependencyStaticShape(option *types.Option) bool {
	shape := *option
	shape.Description, shape.Default = "", ""
	encoded, err := json.Marshal(shape)
	return err == nil && string(encoded) == "{}"
}

func providerDependencyDynamicInput(input ProviderDependencyPreparationInput, name string) bool {
	if _, dynamic := input.DynamicOptions[name]; dynamic {
		return true
	}
	if len(input.StoredOptions[name].Children) != 0 {
		return true
	}
	for _, value := range input.StoredOptions {
		//nolint:modernize // The existing passive-adapter import guard excludes slices.
		for _, child := range value.Children {
			if child == name {
				return true
			}
		}
	}
	return false
}

func prepareProviderDependencyInvocation(
	input ProviderDependencyPreparationInput,
	manifest provider.ProviderConfig,
	binding providerDependencyBinding,
) ProviderDependencyPreparation {
	environment, err := dependencies.NewLookupEnvironment(input.Environment, input.WorkingDirectory)
	if err != nil {
		return providerDependencyPreparationIssue(
			input.Basis,
			"",
			"invalid_invocation_environment",
		)
	}
	value, provenance := providerDependencySelectOption(input, environment, binding)
	origin := dependencies.SurveyDefault
	switch provenance {
	case ProviderDependencyExplicit:
		origin = dependencies.LookupExplicit
	case ProviderDependencyEnvironment:
		origin = dependencies.LookupExplicitEnv
	}
	request := &ProviderDependencySurveyRequest{
		Provider: manifest, Options: map[string]config.OptionValue{binding.option: value},
		OptionOrigins:    map[string]dependencies.LookupOrigin{binding.option: origin},
		Environment:      make(map[string]string, len(input.Environment)),
		WorkingDirectory: input.WorkingDirectory, Target: input.Target, Host: input.Host,
	}
	//nolint:modernize // The existing passive-adapter import guard excludes maps.
	for key, value := range input.Environment {
		request.Environment[key] = value
	}
	return ProviderDependencyPreparation{
		Basis: input.Basis, Request: request,
		Provenance: map[string]ProviderDependencyOptionProvenance{binding.option: provenance},
	}
}

func providerDependencySelectOption(
	input ProviderDependencyPreparationInput,
	environment dependencies.LookupEnvironment,
	binding providerDependencyBinding,
) (config.OptionValue, ProviderDependencyOptionProvenance) {
	if input.Basis == ProviderDependencyPreview {
		if value, present := input.ExplicitAssignments[binding.option]; present {
			return providerDependencyUserOption(
				value,
				input.StoredOptions[binding.option],
			), ProviderDependencyExplicit
		}
		prefix := strings.ToUpper(config.EnvProviderPrefix + input.Provider.Name + "_")
		if value, present := environment.Value(prefix + binding.option); present {
			return providerDependencyUserOption(
				value,
				input.StoredOptions[binding.option],
			), ProviderDependencyEnvironment
		}
	}
	if value, present := input.StoredOptions[binding.option]; present {
		provenance := ProviderDependencySavedNonUser
		if value.UserProvided {
			provenance = ProviderDependencySavedUser
		}
		return copyProviderDependencyOption(value), provenance
	}
	return config.OptionValue{Value: binding.command}, ProviderDependencyDefault
}

func providerDependencyUserOption(value string, before config.OptionValue) config.OptionValue {
	before.Value, before.UserProvided, before.Filled = value, true, nil
	return copyProviderDependencyOption(before)
}

func copyProviderDependencyOption(value config.OptionValue) config.OptionValue {
	if value.Children != nil {
		value.Children = append([]string{}, value.Children...)
	}
	if value.Filled != nil {
		filled := *value.Filled
		value.Filled = &filled
	}
	return value
}

func providerDependencyPreparationIssue(
	basis ProviderDependencyBasis,
	option, reason string,
) ProviderDependencyPreparation {
	if basis != ProviderDependencySaved && basis != ProviderDependencyPreview {
		basis = ""
	}
	return ProviderDependencyPreparation{
		Basis:  basis,
		Issues: []ProviderDependencyPreparationIssue{{Option: option, Reason: reason}},
	}
}
