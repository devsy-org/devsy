package parameters

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	storagev1 "github.com/devsy-org/api/pkg/apis/storage/v1"
)

func VerifyValue(value string, parameter storagev1.AppParameter) (any, error) {
	switch parameter.Type {
	case "", "password", "string", "multiline":
		return verifyStringValue(value, parameter)
	case "boolean":
		return verifyBooleanValue(value, parameter)
	case "number":
		return verifyNumberValue(value, parameter)
	}

	return nil, fmt.Errorf(
		"unrecognized type %s for parameter %s (%s)",
		parameter.Type,
		parameter.Label,
		parameter.Variable,
	)
}

func requiredError(parameter storagev1.AppParameter) error {
	return fmt.Errorf(
		"parameter %s (%s) is required",
		parameter.Label,
		parameter.Variable,
	)
}

func verifyStringValue(value string, parameter storagev1.AppParameter) (any, error) {
	if parameter.DefaultValue != "" && value == "" {
		value = parameter.DefaultValue
	}

	if parameter.Required && value == "" {
		return nil, requiredError(parameter)
	}
	if slices.Contains(parameter.Options, value) {
		return value, nil
	}
	if err := checkValidationRegex(value, parameter); err != nil {
		return nil, err
	}
	if err := checkInvalidationRegex(value, parameter); err != nil {
		return nil, err
	}

	return value, nil
}

func checkValidationRegex(value string, parameter storagev1.AppParameter) error {
	if parameter.Validation == "" {
		return nil
	}

	regEx, err := regexp.Compile(parameter.Validation)
	if err != nil {
		return fmt.Errorf("compile validation regex %s: %w", parameter.Validation, err)
	}

	if !regEx.MatchString(value) {
		return fmt.Errorf(
			"parameter %s (%s) needs to match regex %s",
			parameter.Label,
			parameter.Variable,
			parameter.Validation,
		)
	}

	return nil
}

func checkInvalidationRegex(value string, parameter storagev1.AppParameter) error {
	if parameter.Invalidation == "" {
		return nil
	}

	regEx, err := regexp.Compile(parameter.Invalidation)
	if err != nil {
		return fmt.Errorf(
			"compile invalidation regex %s: %w",
			parameter.Invalidation,
			err,
		)
	}

	if regEx.MatchString(value) {
		return fmt.Errorf(
			"parameter %s (%s) cannot match regex %s",
			parameter.Label,
			parameter.Variable,
			parameter.Invalidation,
		)
	}

	return nil
}

func verifyBooleanValue(value string, parameter storagev1.AppParameter) (any, error) {
	if parameter.DefaultValue != "" && value == "" {
		boolValue, err := strconv.ParseBool(parameter.DefaultValue)
		if err != nil {
			return nil, fmt.Errorf(
				"parse default value for parameter %s (%s): %w",
				parameter.Label,
				parameter.Variable,
				err,
			)
		}

		return boolValue, nil
	}
	if parameter.Required && value == "" {
		return nil, requiredError(parameter)
	}

	boolValue, err := strconv.ParseBool(value)
	if err != nil {
		return nil, fmt.Errorf(
			"parse value for parameter %s (%s): %w",
			parameter.Label,
			parameter.Variable,
			err,
		)
	}
	return boolValue, nil
}

func verifyNumberValue(value string, parameter storagev1.AppParameter) (any, error) {
	if parameter.DefaultValue != "" && value == "" {
		intValue, err := strconv.Atoi(parameter.DefaultValue)
		if err != nil {
			return nil, fmt.Errorf(
				"parse default value for parameter %s (%s): %w",
				parameter.Label,
				parameter.Variable,
				err,
			)
		}

		return intValue, nil
	}
	if parameter.Required && value == "" {
		return nil, requiredError(parameter)
	}
	num, err := strconv.Atoi(value)
	if err != nil {
		return nil, fmt.Errorf(
			"parse value for parameter %s (%s): %w",
			parameter.Label,
			parameter.Variable,
			err,
		)
	}
	if err := checkNumberBounds(num, parameter); err != nil {
		return nil, err
	}

	return num, nil
}

func checkNumberBounds(num int, parameter storagev1.AppParameter) error {
	if parameter.Min != nil && num < *parameter.Min {
		return fmt.Errorf(
			"parameter %s (%s) cannot be smaller than %d",
			parameter.Label,
			parameter.Variable,
			*parameter.Min,
		)
	}
	if parameter.Max != nil && num > *parameter.Max {
		return fmt.Errorf(
			"parameter %s (%s) cannot be greater than %d",
			parameter.Label,
			parameter.Variable,
			*parameter.Max,
		)
	}

	return nil
}

func GetDeepValue(parameters any, path string) any {
	if parameters == nil {
		return nil
	}

	pathSegments := strings.Split(path, ".")
	switch t := parameters.(type) {
	case map[string]any:
		return getDeepValueFromMap(t, pathSegments)
	case []any:
		return getDeepValueFromSlice(t, pathSegments)
	}

	return nil
}

func getDeepValueFromMap(t map[string]any, pathSegments []string) any {
	val, ok := t[pathSegments[0]]
	if !ok {
		return nil
	} else if len(pathSegments) == 1 {
		return val
	}

	return GetDeepValue(val, strings.Join(pathSegments[1:], "."))
}

func getDeepValueFromSlice(t []any, pathSegments []string) any {
	index, err := strconv.Atoi(pathSegments[0])
	if err != nil {
		return nil
	} else if index < 0 || index >= len(t) {
		return nil
	}

	val := t[index]
	if len(pathSegments) == 1 {
		return val
	}

	return GetDeepValue(val, strings.Join(pathSegments[1:], "."))
}
