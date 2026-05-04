package feature

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
)

const optionTypeBoolean = "boolean"

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
