package resolver

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/types"
	"github.com/stretchr/testify/suite"
)

type ValidateUserValueTestSuite struct {
	suite.Suite
}

func TestValidateUserValueTestSuite(t *testing.T) {
	suite.Run(t, new(ValidateUserValueTestSuite))
}

func (suite *ValidateUserValueTestSuite) TestValidateUserValueBoundaries() {
	dev := types.OptionEnumArray{{Value: "dev"}}
	cases := map[string]struct {
		value     string
		option    *types.Option
		wantError bool
	}{
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
		"number accepts negative":        {"-42", &types.Option{Type: "number"}, false},
		"number rejects decimal":         {"1.5", &types.Option{Type: "number"}, true},
		"number accepts int64 max": {
			"9223372036854775807",
			&types.Option{Type: "number"},
			false,
		},
		"number rejects int64 overflow": {
			"9223372036854775808",
			&types.Option{Type: "number"},
			true,
		},
		"boolean accepts 1":            {"1", &types.Option{Type: "boolean"}, false},
		"boolean rejects yes":          {"yes", &types.Option{Type: "boolean"}, true},
		"duration accepts hours":       {"24h", &types.Option{Type: "duration"}, false},
		"duration rejects bare number": {"10", &types.Option{Type: "duration"}, true},
		"unknown type is not checked":  {"anything", &types.Option{Type: "string"}, false},
		"pattern passes but enum excludes": {
			"7",
			&types.Option{ValidationPattern: "^[0-9]+$", Enum: types.OptionEnumArray{{Value: "5"}}},
			true,
		},
		"enum passes but type fails": {
			"yes",
			&types.Option{Type: "boolean", Enum: types.OptionEnumArray{{Value: "yes"}}},
			true,
		},
		"all checks pass": {
			"5",
			&types.Option{
				Type:              "number",
				ValidationPattern: "^[0-9]+$",
				Enum:              types.OptionEnumArray{{Value: "5"}},
			},
			false,
		},
	}

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

func (suite *ValidateUserValueTestSuite) TestPatternErrorTakesPrecedence() {
	option := &types.Option{
		ValidationPattern: "^[0-9]+$",
		ValidationMessage: "must be digits",
		Type:              "number",
		Enum:              types.OptionEnumArray{{Value: "5"}},
	}

	suite.EqualError(validateUserValue("opt", "x", option), "must be digits")
}
