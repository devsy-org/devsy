package feature

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
)

const (
	optionTypeBoolean = "boolean"
	optionTypeSecret  = "secret"
)

// SecretOptions holds configuration for resolving secret-typed feature options.
type SecretOptions struct {
	SecretsFile string
}

// ResolveSecretOptions resolves secret-typed options for a feature. For each option
// with type "secret", it checks (in order): user-provided value, environment variable,
// secrets file, default value. Returns an error if a secret is required but not provided.
func ResolveSecretOptions(
	featureID string,
	featureCfg *config.FeatureConfig,
	userOptions map[string]any,
	opts *SecretOptions,
) (map[string]any, error) {
	if featureCfg == nil || len(featureCfg.Options) == 0 {
		return userOptions, nil
	}

	resolver, err := newSecretResolver(featureID, opts)
	if err != nil {
		return nil, err
	}

	result := make(map[string]any, len(userOptions))
	maps.Copy(result, userOptions)

	for name, option := range featureCfg.Options {
		if option.Type != optionTypeSecret {
			continue
		}
		if _, ok := result[name]; ok {
			continue
		}
		val, err := resolver.resolve(name, option)
		if err != nil {
			return nil, err
		}
		result[name] = val
	}

	return result, nil
}

type secretResolver struct {
	featureID     string
	featureSafeID string
	fileData      map[string]map[string]string
}

func newSecretResolver(featureID string, opts *SecretOptions) (*secretResolver, error) {
	r := &secretResolver{
		featureID:     featureID,
		featureSafeID: getFeatureSafeID(featureID),
	}
	if opts != nil && opts.SecretsFile != "" {
		var err error
		r.fileData, err = parseFeatureSecretsFile(opts.SecretsFile)
		if err != nil {
			return nil, err
		}
	}
	return r, nil
}

func (r *secretResolver) resolve(name string, option config.FeatureConfigOption) (string, error) {
	envVarName := "DEVCONTAINER_FEATURE_SECRET_" + r.featureSafeID + "_" + getFeatureSafeID(name)
	if envVal := os.Getenv(envVarName); envVal != "" {
		return envVal, nil
	}

	if val, ok := r.lookupFileSecret(name); ok {
		return val, nil
	}

	if string(option.Default) != "" {
		return string(option.Default), nil
	}

	return "", fmt.Errorf(
		"feature %q: secret option %q is required but no value was provided. "+
			"Set via devcontainer.json options, environment variable %s, or --feature-secrets-file",
		r.featureID, name, envVarName,
	)
}

func (r *secretResolver) lookupFileSecret(name string) (string, bool) {
	if r.fileData == nil {
		return "", false
	}
	featureSecrets, ok := r.fileData[r.featureID]
	if !ok {
		return "", false
	}
	val, ok := featureSecrets[name]
	return val, ok
}

// IsSecretOption returns true if the given option has type "secret".
func IsSecretOption(option config.FeatureConfigOption) bool {
	return option.Type == optionTypeSecret
}

func parseFeatureSecretsFile(path string) (map[string]map[string]string, error) {
	data, err := os.ReadFile(
		path,
	) // #nosec G304 -- User-specified secrets file path is intentional.
	if err != nil {
		return nil, fmt.Errorf("read feature secrets file: %w", err)
	}

	var secrets map[string]map[string]string
	if err := json.Unmarshal(data, &secrets); err != nil {
		return nil, fmt.Errorf("parse feature secrets file %s: %w", path, err)
	}

	return secrets, nil
}

func getFeatureEnvVariables(feature *config.FeatureConfig, featureOptions any) []string {
	options := getFeatureValueObject(feature, featureOptions)
	variables := []string{}
	for k, v := range options {
		variables = append(variables, fmt.Sprintf(`%s="%v"`, getFeatureSafeID(k), v))
	}

	sort.Strings(variables)

	return variables
}

func getFeatureValueObject(feature *config.FeatureConfig, featureOptions any) map[string]any {
	defaults := getFeatureDefaults(feature)
	switch t := featureOptions.(type) {
	case map[string]any:
		maps.Copy(defaults, t)

		return defaults
	case string:
		if feature.Options == nil {
			return defaults
		}

		_, ok := feature.Options["version"]
		if ok {
			defaults["version"] = t
		}

		return defaults
	}

	return defaults
}

func getFeatureDefaults(feature *config.FeatureConfig) map[string]any {
	ret := map[string]any{}
	for k, v := range feature.Options {
		ret[k] = string(v.Default)
	}

	return ret
}

// ValidateFeatureOptions checks that user-provided option values satisfy
// the type and enum constraints declared in the feature configuration.
func ValidateFeatureOptions(
	featureID string,
	featureCfg *config.FeatureConfig,
	userOptions any,
) error {
	if featureCfg == nil || len(featureCfg.Options) == 0 {
		return nil
	}

	optionsMap := toOptionsMap(userOptions, featureCfg)
	if len(optionsMap) == 0 {
		return nil
	}

	for name, value := range optionsMap {
		option, exists := featureCfg.Options[name]
		if !exists {
			continue
		}

		if err := validateOptionValue(featureID, name, option, value); err != nil {
			return err
		}
	}

	return nil
}

func validateOptionValue(
	featureID, name string,
	option config.FeatureConfigOption,
	value any,
) error {
	strVal := fmt.Sprintf("%v", value)

	if option.Type == optionTypeBoolean {
		if !strings.EqualFold(strVal, "true") && !strings.EqualFold(strVal, "false") {
			return fmt.Errorf(
				"feature %q option %q: must be true or false, got %q",
				featureID, name, strVal,
			)
		}
	}

	if len(option.Enum) > 0 && !slices.Contains(option.Enum, strVal) {
		return fmt.Errorf(
			"feature %q option %q: must be one of %v, got %q",
			featureID, name, option.Enum, strVal,
		)
	}

	return nil
}

func toOptionsMap(userOptions any, featureCfg *config.FeatureConfig) map[string]any {
	switch t := userOptions.(type) {
	case map[string]any:
		return t
	case string:
		if featureCfg.Options == nil {
			return nil
		}
		if _, ok := featureCfg.Options["version"]; ok {
			return map[string]any{"version": t}
		}
		return nil
	}
	return nil
}
