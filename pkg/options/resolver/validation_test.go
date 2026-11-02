package resolver

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/types"
	"github.com/stretchr/testify/suite"
)

const (
	typeNumber  = "number"
	typeBoolean = "boolean"
	digitsRegex = "^[0-9]+$"
	yesValue    = "yes"
)

type ValidateUserValueTestSuite struct {
	suite.Suite
}

type userValueCase struct {
	value     string
	option    *types.Option
	wantError bool
}

func TestValidateUserValueTestSuite(t *testing.T) {
	suite.Run(t, new(ValidateUserValueTestSuite))
}

func (suite *ValidateUserValueTestSuite) TestValidateUserValuePatternAndEnum() {
	dev := types.OptionEnumArray{{Value: "dev"}}
	suite.runUserValueCases(map[string]userValueCase{
		"pattern mismatch rejected": {
			"Abc",
			&types.Option{ValidationPattern: "^[a-z]+$"},
			true,
		},
		"pattern match accepted": {
			"abc",
			&types.Option{ValidationPattern: "^[a-z]+$"},
			false,
		},
		"invalid regex pattern rejected": {"x", &types.Option{ValidationPattern: "[a-"}, true},
		"enum prefix of member rejected": {"de", &types.Option{Enum: dev}, true},
		"enum member accepted":           {"dev", &types.Option{Enum: dev}, false},
		"empty enum accepts any value":   {"anything", &types.Option{}, false},
		"pattern passes but enum excludes": {
			"7",
			&types.Option{
				ValidationPattern: digitsRegex,
				Enum:              types.OptionEnumArray{{Value: "5"}},
			},
			true,
		},
		"all checks pass": {
			"5",
			&types.Option{
				Type:              typeNumber,
				ValidationPattern: digitsRegex,
				Enum:              types.OptionEnumArray{{Value: "5"}},
			},
			false,
		},
	})
}

func (suite *ValidateUserValueTestSuite) TestValidateUserValueTypes() {
	suite.runUserValueCases(map[string]userValueCase{
		"number accepts negative": {"-42", &types.Option{Type: typeNumber}, false},
		"number rejects decimal":  {"1.5", &types.Option{Type: typeNumber}, true},
		"number accepts int64 max": {
			"9223372036854775807",
			&types.Option{Type: typeNumber},
			false,
		},
		"number rejects int64 overflow": {
			"9223372036854775808",
			&types.Option{Type: typeNumber},
			true,
		},
		"boolean accepts 1":            {"1", &types.Option{Type: typeBoolean}, false},
		"boolean rejects yes":          {yesValue, &types.Option{Type: typeBoolean}, true},
		"duration accepts hours":       {"24h", &types.Option{Type: "duration"}, false},
		"duration rejects bare number": {"10", &types.Option{Type: "duration"}, true},
		"unknown type is not checked":  {"anything", &types.Option{Type: "string"}, false},
		"enum passes but type fails": {
			yesValue,
			&types.Option{Type: typeBoolean, Enum: types.OptionEnumArray{{Value: yesValue}}},
			true,
		},
	})
}

func (suite *ValidateUserValueTestSuite) TestPatternErrorTakesPrecedence() {
	option := &types.Option{
		ValidationPattern: digitsRegex,
		ValidationMessage: "must be digits",
		Type:              typeNumber,
		Enum:              types.OptionEnumArray{{Value: "5"}},
	}

	suite.EqualError(validateUserValue("opt", "x", option), "must be digits")
}

func (suite *ValidateUserValueTestSuite) runUserValueCases(cases map[string]userValueCase) {
	for name, tc := range cases {
		suite.Run(name, func() {
			err := validateUserValue("opt", tc.value, tc.option)
			if tc.wantError {
				suite.Error(err)
			} else {
				suite.NoError(err)
			}
		})
	}
}
