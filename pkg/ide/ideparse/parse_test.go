package ideparse

import (
	"errors"
	"reflect"
	"regexp/syntax"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/ide"
	"github.com/devsy-org/devsy/pkg/provider"
)

const (
	ideOpenVSCode = "openvscode"
	ideVSCode     = "vscode"
)

// setupTempHome redirects the path manager to a temp HOME so
// SaveWorkspaceConfig writes under the test's tempdir.
func setupTempHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	config.ResetPathManager()
	t.Cleanup(config.ResetPathManager)
}

func emptyConfig() *config.Config {
	return &config.Config{
		DefaultContext: config.DefaultContext,
		Contexts: map[string]*config.ContextConfig{
			config.DefaultContext: {},
		},
	}
}

// TestRefreshIDEOptions_SwitchPersistsToDisk locks down bug #3: switching
// from openvscode -> vscode on an existing workspace must update
// workspace.IDE.Name AND write the new value to workspace.json so the next
// `opener.Open` dispatch resolves to the right IDE.
func TestRefreshIDEOptions_SwitchPersistsToDisk(t *testing.T) {
	setupTempHome(t)

	ws := &provider.Workspace{
		ID:      "ws-1",
		Context: config.DefaultContext,
		IDE: provider.WorkspaceIDEConfig{
			Name: ideOpenVSCode,
		},
	}
	if err := provider.SaveWorkspaceConfig(ws); err != nil {
		t.Fatalf("seed save: %v", err)
	}

	got, err := RefreshIDEOptions(emptyConfig(), ws, ideVSCode, nil)
	if err != nil {
		t.Fatalf("RefreshIDEOptions: %v", err)
	}
	if got.IDE.Name != ideVSCode {
		t.Errorf("returned workspace.IDE.Name = %q, want %q", got.IDE.Name, ideVSCode)
	}

	reloaded, err := provider.LoadWorkspaceConfig(config.DefaultContext, "ws-1")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.IDE.Name != ideVSCode {
		t.Errorf(
			"on-disk workspace.IDE.Name = %q, want %q (the switch did not persist)",
			reloaded.IDE.Name, ideVSCode,
		)
	}
}

// TestRefreshIDEOptions_EmptyIDEKeepsExisting ensures that calling with
// ide="" does NOT clobber an already-configured workspace IDE (this is the
// default `devsy up <id>` behavior when --ide is not supplied).
func TestRefreshIDEOptions_EmptyIDEKeepsExisting(t *testing.T) {
	setupTempHome(t)

	ws := &provider.Workspace{
		ID:      "ws-2",
		Context: config.DefaultContext,
		IDE: provider.WorkspaceIDEConfig{
			Name: ideOpenVSCode,
		},
	}
	if err := provider.SaveWorkspaceConfig(ws); err != nil {
		t.Fatalf("seed save: %v", err)
	}

	got, err := RefreshIDEOptions(emptyConfig(), ws, "", nil)
	if err != nil {
		t.Fatalf("RefreshIDEOptions: %v", err)
	}
	if got.IDE.Name != ideOpenVSCode {
		t.Errorf("returned workspace.IDE.Name = %q, want %q", got.IDE.Name, ideOpenVSCode)
	}
}

const (
	testOptionKey       = "OPTION"
	testLettersPattern  = `^[a-z]+$`
	testAllowedValue    = "allowed"
	testSecondEnumValue = "Second"
	testInvalidPattern  = `[abc`
)

type optionValidationCase struct {
	name    string
	option  ide.Option
	key     string
	value   string
	wantErr string
}

func TestValidateOptionValue(t *testing.T) {
	t.Run("unconstrained empty value ignores message", func(t *testing.T) {
		assertValidationCases(t, []optionValidationCase{{
			option: ide.Option{ValidationMessage: "ignored"},
			key:    testOptionKey,
			value:  "",
		}})
	})
	t.Run("regex behavior", testValidateRegexCases)
	t.Run("enum behavior", testValidateEnumCases)
	t.Run("regex precedes enum", testValidateOrderCases)
}

func testValidateRegexCases(t *testing.T) {
	assertValidationCases(t, []optionValidationCase{
		{
			name:   "complete match",
			option: ide.Option{ValidationPattern: testLettersPattern},
			key:    testOptionKey,
			value:  "value",
		},
		{
			name:   "unanchored MatchString",
			option: ide.Option{ValidationPattern: `abc`},
			key:    testOptionKey,
			value:  "xabcx",
		},
		{
			name:    "default mismatch error",
			option:  ide.Option{ValidationPattern: testLettersPattern},
			key:     testOptionKey,
			value:   "123",
			wantErr: `invalid value "123" for option "OPTION", has to match the following regEx: ^[a-z]+$`,
		},
		{
			name: "custom message is literal with percent signs",
			option: ide.Option{
				ValidationPattern: `^ok$`,
				ValidationMessage: "bad %s value: 100%",
			},
			key:     testOptionKey,
			value:   "no",
			wantErr: "bad %s value: 100%",
		},
	})
}

func testValidateEnumCases(t *testing.T) {
	values := []string{"First", testSecondEnumValue}
	assertValidationCases(t, []optionValidationCase{
		{
			name:   "accepts exact value",
			option: ide.Option{Enum: values},
			key:    testOptionKey,
			value:  testSecondEnumValue,
		},
		{
			name:    "case sensitive and display order preserved",
			option:  ide.Option{Enum: values},
			key:     testOptionKey,
			value:   "second",
			wantErr: `invalid value "second" for option "OPTION", has to match one of the following values: [First Second]`,
		},
	})
}

func testValidateOrderCases(t *testing.T) {
	assertValidationCases(t, []optionValidationCase{
		{
			name: "regex failure precedes enum failure",
			option: ide.Option{
				ValidationPattern: `^match$`,
				Enum:              []string{testAllowedValue},
			},
			key:     testOptionKey,
			value:   "other",
			wantErr: `invalid value "other" for option "OPTION", has to match the following regEx: ^match$`,
		},
		{
			name: "regex success is followed by enum rejection",
			option: ide.Option{
				ValidationPattern: testLettersPattern,
				Enum:              []string{testAllowedValue},
			},
			key:     testOptionKey,
			value:   "other",
			wantErr: `invalid value "other" for option "OPTION", has to match one of the following values: [allowed]`,
		},
		{
			name: "regex success is followed by enum acceptance",
			option: ide.Option{
				ValidationPattern: testLettersPattern,
				Enum:              []string{testAllowedValue},
			},
			key:   testOptionKey,
			value: testAllowedValue,
		},
	})
}

func assertValidationCases(t *testing.T, cases []optionValidationCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateOptionValue(tc.option, tc.key, tc.value)
			assertError(t, err, tc.wantErr)
		})
	}
}

func TestValidateOptionValueInvalidPattern(t *testing.T) {
	option := ide.Option{
		ValidationPattern: testInvalidPattern,
		ValidationMessage: "custom validation message",
	}
	err := validateOptionValue(option, testOptionKey, "value")
	syntaxErr, ok := errors.AsType[*syntax.Error](err)
	if !ok {
		t.Fatalf("validateOptionValue() error = %T (%v), want *syntax.Error", err, err)
	}
	if err.Error() != "error parsing regexp: missing closing ]: `[abc`" {
		t.Errorf("validateOptionValue() error = %q, want regexp syntax error", err)
	}
	if syntaxErr.Expr != testInvalidPattern {
		t.Errorf("syntax error expression = %q, want %q", syntaxErr.Expr, testInvalidPattern)
	}
}

func TestParseOptions(t *testing.T) {
	t.Run("normalization and values", testParseOptionValues)
	t.Run("input errors", testParseOptionInputErrors)
	t.Run("validation order", testParseOptionValidation)
	t.Run("invalid pattern preserves syntax error", testParseInvalidPattern)
}

type parseOptionsCase struct {
	name        string
	options     []string
	definitions ide.Options
	want        map[string]config.OptionValue
	wantErr     string
}

func testParseOptionValues(t *testing.T) {
	assertParseOptionsCases(t, []parseOptionsCase{
		{
			name:        "trimmed key is uppercased",
			options:     []string{"  editor  =value"},
			definitions: ide.Options{"EDITOR": {}},
			want: map[string]config.OptionValue{
				"EDITOR": {Value: "value", UserProvided: true},
			},
		},
		{
			name:        "value whitespace and extra equals are preserved",
			options:     []string{testOptionKey + "= value with spaces =tail "},
			definitions: ide.Options{testOptionKey: {}},
			want: map[string]config.OptionValue{
				testOptionKey: {Value: " value with spaces =tail ", UserProvided: true},
			},
		},
		{
			name:        "empty value is allowed",
			options:     []string{testOptionKey + "="},
			definitions: ide.Options{testOptionKey: {}},
			want: map[string]config.OptionValue{
				testOptionKey: {Value: "", UserProvided: true},
			},
		},
		{
			name:        "duplicate key uses last value",
			options:     []string{testOptionKey + "=first", "option=last"},
			definitions: ide.Options{testOptionKey: {}},
			want: map[string]config.OptionValue{
				testOptionKey: {Value: "last", UserProvided: true},
			},
		},
		{
			name:        "nil definitions allow no options",
			definitions: nil,
			want:        map[string]config.OptionValue{},
		},
	})
}

func testParseOptionInputErrors(t *testing.T) {
	assertParseOptionsCases(t, []parseOptionsCase{
		{
			name:        "missing equals is rejected",
			options:     []string{testOptionKey},
			definitions: ide.Options{testOptionKey: {}},
			wantErr:     `invalid option "OPTION", expected format KEY=VALUE`,
		},
		{
			name:        "unknown option has deterministic one-item list",
			options:     []string{"UNKNOWN=value"},
			definitions: ide.Options{"KNOWN": {}},
			wantErr:     `invalid option "UNKNOWN", allowed options are: [KNOWN]`,
		},
	})
}

func testParseOptionValidation(t *testing.T) {
	assertParseOptionsCases(t, []parseOptionsCase{
		{
			name:    "validation error returns nil map after earlier valid option",
			options: []string{"FIRST=valid", "SECOND=invalid"},
			definitions: ide.Options{
				"FIRST":  {},
				"SECOND": {ValidationPattern: `^valid$`},
			},
			wantErr: `invalid value "invalid" for option "SECOND", has to match the following regEx: ^valid$`,
		},
		{
			name:    "regex success can still fail enum",
			options: []string{testOptionKey + "=other"},
			definitions: ide.Options{
				testOptionKey: {
					ValidationPattern: testLettersPattern,
					Enum:              []string{testAllowedValue},
				},
			},
			wantErr: `invalid value "other" for option "OPTION", has to match one of the following values: [allowed]`,
		},
		{
			name:    "regex failure takes precedence when enum accepts",
			options: []string{testOptionKey + "=" + testAllowedValue},
			definitions: ide.Options{
				testOptionKey: {ValidationPattern: `^other$`, Enum: []string{testAllowedValue}},
			},
			wantErr: `invalid value "allowed" for option "OPTION", has to match the following regEx: ^other$`,
		},
	})
}

func testParseInvalidPattern(t *testing.T) {
	got, err := ParseOptions(
		[]string{testOptionKey + "=value"},
		ide.Options{
			testOptionKey: {
				ValidationPattern: testInvalidPattern,
				ValidationMessage: "custom message",
			},
		},
	)
	_, ok := errors.AsType[*syntax.Error](err)
	if !ok {
		t.Fatalf("ParseOptions() error = %T (%v), want *syntax.Error", err, err)
	}
	if got != nil {
		t.Errorf("ParseOptions() map = %#v, want nil on error", got)
	}
}

func assertParseOptionsCases(t *testing.T, cases []parseOptionsCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseOptions(tc.options, tc.definitions)
			assertError(t, err, tc.wantErr)
			if tc.wantErr != "" {
				if got != nil {
					t.Errorf("ParseOptions() map = %#v, want nil on error", got)
				}
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ParseOptions() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func assertError(t *testing.T, err error, want string) {
	t.Helper()
	if want == "" {
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
		return
	}
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}
