package feature

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/stretchr/testify/suite"
)

const (
	testFeatureID     = "ghcr.io/devcontainers/features/go"
	testOptionName    = "version"
	testOptionInstall = "install"
	optionTypeString  = "string"
	testEnumVal120    = "1.20"
	testEnumVal121    = "1.21"
	testEnumValLatest = "latest"
)

type OptionsTestSuite struct {
	suite.Suite
}

func TestOptionsTestSuite(t *testing.T) {
	suite.Run(t, new(OptionsTestSuite))
}

func (s *OptionsTestSuite) TestValidBooleanStringTrue() {
	cfg := &config.FeatureConfig{
		Options: map[string]config.FeatureConfigOption{
			testOptionInstall: {Type: optionTypeBoolean},
		},
	}
	err := ValidateFeatureOptions(testFeatureID, cfg, map[string]any{testOptionInstall: "true"})
	s.NoError(err)
}

func (s *OptionsTestSuite) TestValidBooleanStringFalse() {
	cfg := &config.FeatureConfig{
		Options: map[string]config.FeatureConfigOption{
			testOptionInstall: {Type: optionTypeBoolean},
		},
	}
	err := ValidateFeatureOptions(testFeatureID, cfg, map[string]any{testOptionInstall: "false"})
	s.NoError(err)
}

func (s *OptionsTestSuite) TestValidBooleanCaseInsensitive() {
	cfg := &config.FeatureConfig{
		Options: map[string]config.FeatureConfigOption{
			testOptionInstall: {Type: optionTypeBoolean},
		},
	}
	err := ValidateFeatureOptions(testFeatureID, cfg, map[string]any{testOptionInstall: "True"})
	s.NoError(err)

	err = ValidateFeatureOptions(testFeatureID, cfg, map[string]any{testOptionInstall: "FALSE"})
	s.NoError(err)
}

func (s *OptionsTestSuite) TestValidBooleanGoBoolTrue() {
	cfg := &config.FeatureConfig{
		Options: map[string]config.FeatureConfigOption{
			testOptionInstall: {Type: optionTypeBoolean},
		},
	}
	err := ValidateFeatureOptions(testFeatureID, cfg, map[string]any{testOptionInstall: true})
	s.NoError(err)
}

func (s *OptionsTestSuite) TestValidBooleanGoBoolFalse() {
	cfg := &config.FeatureConfig{
		Options: map[string]config.FeatureConfigOption{
			testOptionInstall: {Type: optionTypeBoolean},
		},
	}
	err := ValidateFeatureOptions(testFeatureID, cfg, map[string]any{testOptionInstall: false})
	s.NoError(err)
}

func (s *OptionsTestSuite) TestInvalidBooleanYes() {
	cfg := &config.FeatureConfig{
		Options: map[string]config.FeatureConfigOption{
			testOptionInstall: {Type: optionTypeBoolean},
		},
	}
	err := ValidateFeatureOptions(testFeatureID, cfg, map[string]any{testOptionInstall: "yes"})
	s.Error(err)
	s.Contains(err.Error(), "must be true or false")
	s.Contains(err.Error(), `got "yes"`)
}

func (s *OptionsTestSuite) TestInvalidBooleanOne() {
	cfg := &config.FeatureConfig{
		Options: map[string]config.FeatureConfigOption{
			testOptionInstall: {Type: optionTypeBoolean},
		},
	}
	err := ValidateFeatureOptions(testFeatureID, cfg, map[string]any{testOptionInstall: "1"})
	s.Error(err)
	s.Contains(err.Error(), "must be true or false")
}

func (s *OptionsTestSuite) TestInvalidBooleanMaybe() {
	cfg := &config.FeatureConfig{
		Options: map[string]config.FeatureConfigOption{
			testOptionInstall: {Type: optionTypeBoolean},
		},
	}
	err := ValidateFeatureOptions(testFeatureID, cfg, map[string]any{testOptionInstall: "maybe"})
	s.Error(err)
	s.Contains(err.Error(), "must be true or false")
	s.Contains(err.Error(), `got "maybe"`)
}

func (s *OptionsTestSuite) TestValidEnumValue() {
	cfg := &config.FeatureConfig{
		Options: map[string]config.FeatureConfigOption{
			testOptionName: {
				Type: optionTypeString,
				Enum: []string{testEnumVal120, testEnumVal121, testEnumValLatest},
			},
		},
	}
	err := ValidateFeatureOptions(testFeatureID, cfg, map[string]any{testOptionName: "1.21"})
	s.NoError(err)
}

func (s *OptionsTestSuite) TestInvalidEnumValue() {
	cfg := &config.FeatureConfig{
		Options: map[string]config.FeatureConfigOption{
			testOptionName: {
				Type: optionTypeString,
				Enum: []string{testEnumVal120, testEnumVal121, testEnumValLatest},
			},
		},
	}
	err := ValidateFeatureOptions(testFeatureID, cfg, map[string]any{testOptionName: "1.19"})
	s.Error(err)
	s.Contains(err.Error(), "must be one of")
	s.Contains(err.Error(), `got "1.19"`)
	s.Contains(err.Error(), testEnumVal120)
}

func (s *OptionsTestSuite) TestStringTypeNoEnum() {
	cfg := &config.FeatureConfig{
		Options: map[string]config.FeatureConfigOption{
			testOptionName: {Type: optionTypeString},
		},
	}
	err := ValidateFeatureOptions(
		testFeatureID,
		cfg,
		map[string]any{testOptionName: "anything-goes"},
	)
	s.NoError(err)
}

func (s *OptionsTestSuite) TestNoTypeNoEnum() {
	cfg := &config.FeatureConfig{
		Options: map[string]config.FeatureConfigOption{
			testOptionName: {},
		},
	}
	err := ValidateFeatureOptions(testFeatureID, cfg, map[string]any{testOptionName: "whatever"})
	s.NoError(err)
}

func (s *OptionsTestSuite) TestNilUserOptions() {
	cfg := &config.FeatureConfig{
		Options: map[string]config.FeatureConfigOption{
			testOptionName: {Type: optionTypeBoolean},
		},
	}
	err := ValidateFeatureOptions(testFeatureID, cfg, nil)
	s.NoError(err)
}

func (s *OptionsTestSuite) TestEmptyMapUserOptions() {
	cfg := &config.FeatureConfig{
		Options: map[string]config.FeatureConfigOption{
			testOptionName: {Type: optionTypeBoolean},
		},
	}
	err := ValidateFeatureOptions(testFeatureID, cfg, map[string]any{})
	s.NoError(err)
}

func (s *OptionsTestSuite) TestNilFeatureConfig() {
	err := ValidateFeatureOptions(testFeatureID, nil, map[string]any{testOptionName: "value"})
	s.NoError(err)
}

func (s *OptionsTestSuite) TestEmptyOptionsMap() {
	cfg := &config.FeatureConfig{
		Options: map[string]config.FeatureConfigOption{},
	}
	err := ValidateFeatureOptions(testFeatureID, cfg, map[string]any{testOptionName: "value"})
	s.NoError(err)
}

func (s *OptionsTestSuite) TestStringShorthandValidatesVersion() {
	cfg := &config.FeatureConfig{
		Options: map[string]config.FeatureConfigOption{
			testOptionName: {
				Type: optionTypeString,
				Enum: []string{testEnumVal120, testEnumVal121, testEnumValLatest},
			},
		},
	}
	err := ValidateFeatureOptions(testFeatureID, cfg, "1.21")
	s.NoError(err)

	err = ValidateFeatureOptions(testFeatureID, cfg, "1.19")
	s.Error(err)
	s.Contains(err.Error(), "must be one of")
}

func (s *OptionsTestSuite) TestStringShorthandNoVersionOption() {
	cfg := &config.FeatureConfig{
		Options: map[string]config.FeatureConfigOption{
			testOptionInstall: {Type: optionTypeBoolean},
		},
	}
	// String shorthand with no "version" option defined - no validation
	err := ValidateFeatureOptions(testFeatureID, cfg, "anything")
	s.NoError(err)
}

func (s *OptionsTestSuite) TestUnknownOptionIgnored() {
	cfg := &config.FeatureConfig{
		Options: map[string]config.FeatureConfigOption{
			testOptionName: {
				Type: optionTypeString,
				Enum: []string{testEnumVal120, testEnumVal121},
			},
		},
	}
	err := ValidateFeatureOptions(testFeatureID, cfg, map[string]any{"unknownOpt": "whatever"})
	s.NoError(err)
}

func (s *OptionsTestSuite) TestErrorContainsFeatureID() {
	cfg := &config.FeatureConfig{
		Options: map[string]config.FeatureConfigOption{
			testOptionInstall: {Type: optionTypeBoolean},
		},
	}
	err := ValidateFeatureOptions(testFeatureID, cfg, map[string]any{testOptionInstall: "bad"})
	s.Error(err)
	s.Contains(err.Error(), testFeatureID)
	s.Contains(err.Error(), "install")
}
