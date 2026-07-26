package resolver

import (
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/types"
)

func printUnusedUserValues(
	userValues map[string]string,
	options config.OptionDefinitions,
) {
	allowedOptions := []string{}
	for k := range options {
		allowedOptions = append(allowedOptions, k)
	}

	for k := range userValues {
		if options[k] == nil {
			log.Warnf(
				"Option %s was specified but is not defined, allowed options are %v",
				k,
				allowedOptions,
			)
		}
	}
}

func validateUserValue(optionName, userValue string, option *types.Option) error {
	if err := validateUserValuePattern(optionName, userValue, option); err != nil {
		return err
	}

	if err := validateUserValueEnum(optionName, userValue, option); err != nil {
		return err
	}

	return validateUserValueType(optionName, userValue, option)
}

func validateUserValuePattern(optionName, userValue string, option *types.Option) error {
	if option.ValidationPattern == "" {
		return nil
	}

	matcher, err := regexp.Compile(option.ValidationPattern)
	if err != nil {
		return err
	}

	if matcher.MatchString(userValue) {
		return nil
	}

	if option.ValidationMessage != "" {
		return fmt.Errorf("%s", option.ValidationMessage)
	}

	return fmt.Errorf(
		"invalid value %q for option %q, has to match the following regEx: %s",
		userValue,
		optionName,
		option.ValidationPattern,
	)
}

func validateUserValueEnum(optionName, userValue string, option *types.Option) error {
	if len(option.Enum) == 0 {
		return nil
	}

	for _, e := range option.Enum {
		if userValue == e.Value {
			return nil
		}
	}

	return fmt.Errorf(
		"invalid value %q for option %q, has to match one of the following values: %v",
		userValue,
		optionName,
		option.Enum,
	)
}

func validateUserValueType(optionName, userValue string, option *types.Option) error {
	switch option.Type {
	case "number":
		if _, err := strconv.ParseInt(userValue, 10, 64); err != nil {
			return fmt.Errorf(
				"invalid value %q for option %q, must be a number",
				userValue,
				optionName,
			)
		}
	case "boolean":
		if _, err := strconv.ParseBool(userValue); err != nil {
			return fmt.Errorf(
				"invalid value %q for option %q, must be a boolean",
				userValue,
				optionName,
			)
		}
	case "duration":
		if _, err := time.ParseDuration(userValue); err != nil {
			return fmt.Errorf(
				"invalid value %q for option %q, must be a duration like 10s, 5m or 24h",
				userValue,
				optionName,
			)
		}
	}

	return nil
}
