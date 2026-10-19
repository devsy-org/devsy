package parameters

import (
	"testing"

	storagev1 "github.com/devsy-org/api/pkg/apis/storage/v1"
	"gotest.tools/assert"
)

const (
	labelName    = "Name"
	labelCount   = "Count"
	labelEnabled = "Enabled"
	varName      = "name"
	varCount     = "count"
	varEnabled   = "enabled"
	valueDev     = "dev"
	valueABC     = "abc"
	valueLegacy  = "legacy"
	valueTrue    = "true"
)

type verifyCase struct {
	name        string
	value       string
	param       storagev1.AppParameter
	want        any
	errContains string
}

func runVerifyCases(
	t *testing.T,
	verify func(string, storagev1.AppParameter) (any, error),
	cases []verifyCase,
) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := verify(tc.value, tc.param)
			if tc.errContains != "" {
				assert.ErrorContains(t, err, tc.errContains)
				return
			}
			assert.NilError(t, err)
			assert.DeepEqual(t, got, tc.want)
		})
	}
}

func TestVerifyValueDispatchesByType(t *testing.T) {
	runVerifyCases(t, VerifyValue, []verifyCase{
		{
			name: "empty type is verified as string", value: "hello",
			param: storagev1.AppParameter{Label: labelName, Variable: varName}, want: "hello",
		},
		{
			name: "password type is verified as string", value: "s3cret",
			param: storagev1.AppParameter{Type: "password"}, want: "s3cret",
		},
		{
			name: "number type is verified as integer", value: "12",
			param: storagev1.AppParameter{Type: "number"}, want: 12,
		},
		{
			name: "unknown type is rejected with parameter identity", value: "2026-10-18",
			param:       storagev1.AppParameter{Type: "date", Label: "When", Variable: "when"},
			errContains: "unrecognized type date for parameter When (when)",
		},
	})
}

var stringValueCases = []verifyCase{
	{
		name:  "empty value falls back to default",
		param: storagev1.AppParameter{DefaultValue: valueDev}, want: valueDev,
	},
	{
		name: "explicit value wins over default", value: "prod",
		param: storagev1.AppParameter{DefaultValue: valueDev}, want: "prod",
	},
	{name: "optional empty value without default is accepted", want: ""},
	{
		name:        "required empty value is rejected",
		param:       storagev1.AppParameter{Label: labelName, Variable: varName, Required: true},
		errContains: "parameter Name (name) is required",
	},
	{
		name:  "required empty value is satisfied by default",
		param: storagev1.AppParameter{Required: true, DefaultValue: valueDev}, want: valueDev,
	},
	{
		name:        "default is validated like user input",
		param:       storagev1.AppParameter{DefaultValue: valueABC, Validation: `^[0-9]+$`},
		errContains: "needs to match regex",
	},
	{
		name: "value matching validation regex is accepted", value: valueABC,
		param: storagev1.AppParameter{Validation: `^[a-z]+$`}, want: valueABC,
	},
	{
		name: "value not matching validation regex is rejected", value: "ABC",
		param:       storagev1.AppParameter{Validation: `^[a-z]+$`},
		errContains: "needs to match regex ^[a-z]+$",
	},
	{
		name: "malformed validation regex is reported", value: "x",
		param:       storagev1.AppParameter{Validation: "["},
		errContains: "compile validation regex [",
	},
	{
		name:  "value matching invalidation regex is rejected",
		value: "a b",
		param: storagev1.AppParameter{
			Invalidation: `\s`,
			Label:        labelName,
			Variable:     varName,
		},
		errContains: "parameter Name (name) cannot match regex",
	},
	{
		name: "option value bypasses validation regex", value: valueLegacy,
		param: storagev1.AppParameter{Options: []string{valueLegacy}, Validation: `^[a-z]{10}$`},
		want:  valueLegacy,
	},
	{
		name: "value outside options still runs validation", value: "c",
		param:       storagev1.AppParameter{Options: []string{"a", "b"}, Validation: `^[a-z]{3}$`},
		errContains: "needs to match regex",
	},
}

func TestVerifyStringValue(t *testing.T) {
	runVerifyCases(t, verifyStringValue, stringValueCases)
}

func TestVerifyBooleanValue(t *testing.T) {
	runVerifyCases(t, verifyBooleanValue, []verifyCase{
		{name: "true literal is parsed", value: valueTrue, want: true},
		{
			name: "non-boolean text is rejected", value: "yes",
			param:       storagev1.AppParameter{Label: labelEnabled, Variable: varEnabled},
			errContains: "parse value for parameter Enabled (enabled)",
		},
		{
			name:  "empty value uses boolean default",
			param: storagev1.AppParameter{DefaultValue: valueTrue}, want: true,
		},
		{
			name: "explicit value overrides boolean default", value: "false",
			param: storagev1.AppParameter{DefaultValue: valueTrue}, want: false,
		},
		{
			name: "malformed boolean default is reported",
			param: storagev1.AppParameter{
				DefaultValue: "maybe", Label: labelEnabled, Variable: varEnabled,
			},
			errContains: "parse default value for parameter Enabled (enabled)",
		},
		{
			name: "required empty boolean is rejected",
			param: storagev1.AppParameter{
				Required: true, Label: labelEnabled, Variable: varEnabled,
			},
			errContains: "parameter Enabled (enabled) is required",
		},
		{
			name:  "required empty boolean is satisfied by default",
			param: storagev1.AppParameter{Required: true, DefaultValue: "false"}, want: false,
		},
	})
}

func TestVerifyNumberValue(t *testing.T) {
	runVerifyCases(t, verifyNumberValue, []verifyCase{
		{name: "integer is parsed", value: "42", want: 42},
		{
			name: "fractional value is rejected", value: "4.5",
			param:       storagev1.AppParameter{Label: labelCount, Variable: varCount},
			errContains: "parse value for parameter Count (count)",
		},
		{
			name:  "empty value uses numeric default",
			param: storagev1.AppParameter{DefaultValue: "7"}, want: 7,
		},
		{
			name: "required empty number is rejected",
			param: storagev1.AppParameter{
				Required: true,
				Label:    labelCount,
				Variable: varCount,
			},
			errContains: "parameter Count (count) is required",
		},
		{
			name: "minimum boundary is accepted", value: "1",
			param: storagev1.AppParameter{Min: new(1)}, want: 1,
		},
		{
			name: "value below minimum is rejected", value: "0",
			param:       storagev1.AppParameter{Min: new(1), Label: labelCount, Variable: varCount},
			errContains: "parameter Count (count) cannot be smaller than 1",
		},
		{
			name: "maximum boundary is accepted", value: "10",
			param: storagev1.AppParameter{Max: new(10)}, want: 10,
		},
		{
			name:  "value above maximum is rejected",
			value: "11",
			param: storagev1.AppParameter{
				Max:      new(10),
				Label:    labelCount,
				Variable: varCount,
			},
			errContains: "parameter Count (count) cannot be greater than 10",
		},
	})
}

func TestGetDeepValue(t *testing.T) {
	nested := map[string]any{"a": map[string]any{"b": map[string]any{"c": 1}}}
	withItems := map[string]any{
		"items": []any{"x", "y"},
		"list":  []any{map[string]any{varName: "first"}},
		"text":  "plain",
	}

	cases := []struct {
		name       string
		parameters any
		path       string
		want       any
	}{
		{name: "nil parameters yield nil", path: "a"},
		{
			name:       "top-level key returns its value",
			parameters: map[string]any{varName: "devsy"},
			path:       varName,
			want:       "devsy",
		},
		{name: "dotted path descends nested maps", parameters: nested, path: "a.b.c", want: 1},
		{
			name:       "numeric segment indexes into slice",
			parameters: withItems,
			path:       "items.1",
			want:       "y",
		},
		{
			name:       "path continues through object inside slice",
			parameters: withItems,
			path:       "list.0.name",
			want:       "first",
		},
		{name: "missing key yields nil", parameters: withItems, path: "absent"},
		{name: "index past end of slice yields nil", parameters: withItems, path: "items.2"},
		{name: "negative index yields nil", parameters: withItems, path: "items.-1"},
		{
			name:       "non-numeric segment on slice yields nil",
			parameters: withItems,
			path:       "items.first",
		},
		{name: "path through scalar yields nil", parameters: withItems, path: "text.length"},
		{name: "unsupported root type yields nil", parameters: "plain", path: "a"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.DeepEqual(t, GetDeepValue(tc.parameters, tc.path), tc.want)
		})
	}
}
