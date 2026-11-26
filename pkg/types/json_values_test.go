package types_test

import (
	"encoding/json"
	"errors"
	"strconv"
	"testing"

	"github.com/devsy-org/devsy/pkg/types"
	"gotest.tools/assert"
)

type decodeCase[T any] struct {
	name   string
	input  string
	expect T
	err    error
}

func runDecodeCases[T any](t *testing.T, cases []decodeCase[T], decode func([]byte) (T, error)) {
	t.Helper()
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := decode([]byte(testCase.input))
			if testCase.err != nil {
				assert.Assert(t, errors.Is(err, testCase.err), "got: %v", err)
				return
			}
			assert.NilError(t, err)
			assert.DeepEqual(t, testCase.expect, got)
		})
	}
}

func TestStrIntArrayUnmarshalJSON(t *testing.T) {
	runDecodeCases(t, []decodeCase[[]string]{
		{name: "string scalar", input: `{"input": "name"}`, expect: []string{"name"}},
		{name: "integer scalar", input: `{"input": 42}`, expect: []string{"42"}},
		{name: "string list", input: `{"input": ["a", "b"]}`, expect: []string{"a", "b"}},
		{name: "mixed list", input: `{"input": ["a", 2]}`, expect: []string{"a", "2"}},
		{name: "empty string list", input: `{"input": []}`, expect: []string{}},
		{name: "boolean scalar", input: `{"input": true}`, err: types.ErrUnsupportedType},
		{name: "nested list", input: `{"input": [["a"]]}`, err: types.ErrUnsupportedType},
	}, func(input []byte) ([]string, error) {
		var data struct {
			Input types.StrIntArray `json:"input"`
		}
		err := json.Unmarshal(input, &data)
		return []string(data.Input), err
	})
}

func TestStrArrayUnmarshalJSON(t *testing.T) {
	runDecodeCases(t, []decodeCase[[]string]{
		{name: "string scalar", input: `{"input": "make build"}`, expect: []string{"make build"}},
		{
			name:   "string list",
			input:  `{"input": ["npm", "install"]}`,
			expect: []string{"npm", "install"},
		},
		{name: "empty list", input: `{"input": []}`, expect: []string{}},
		{name: "integer scalar", input: `{"input": 42}`, err: types.ErrUnsupportedType},
		{name: "integer element", input: `{"input": ["npm", 1]}`, err: types.ErrUnsupportedType},
		{name: "null element", input: `{"input": [null]}`, err: types.ErrUnsupportedType},
	}, func(input []byte) ([]string, error) {
		var data struct {
			Input types.StrArray `json:"input"`
		}
		err := json.Unmarshal(input, &data)
		return []string(data.Input), err
	})
}

func parseStrBool(input []byte) (bool, error) {
	var data struct {
		Local types.StrBool `json:"local"`
	}
	if err := json.Unmarshal(input, &data); err != nil {
		return false, err
	}
	return data.Local.Bool()
}

func TestStrBoolJSONValueParsesToBool(t *testing.T) {
	runDecodeCases(t, []decodeCase[bool]{
		{name: "boolean true", input: `{"local": true}`, expect: true},
		{name: "boolean false", input: `{"local": false}`, expect: false},
		{name: "string true", input: `{"local": "true"}`, expect: true},
		{name: "string one", input: `{"local": "1"}`, expect: true},
		{name: "empty string is unset", input: `{"local": ""}`, expect: false},
		{name: "absent field is unset", input: `{}`, expect: false},
		{name: "string yes", input: `{"local": "yes"}`, err: strconv.ErrSyntax},
		{name: "number", input: `{"local": 1}`, err: types.ErrUnsupportedType},
	}, parseStrBool)
}

func TestStrBoolBoolNilReceiverIsFalse(t *testing.T) {
	var unset *types.StrBool

	got, err := unset.Bool()

	assert.NilError(t, err)
	assert.Equal(t, got, false)
}

func TestOptionEnumArrayUnmarshalJSON(t *testing.T) {
	runDecodeCases(t, []decodeCase[[]types.OptionEnum]{
		{
			name:   "plain values",
			input:  `{"enum": ["dev", "prod"]}`,
			expect: []types.OptionEnum{{Value: "dev"}, {Value: "prod"}},
		},
		{
			name:   "objects with display names",
			input:  `{"enum": [{"value": "dev", "displayName": "Development"}]}`,
			expect: []types.OptionEnum{{Value: "dev", DisplayName: "Development"}},
		},
		{name: "empty enum list", input: `{"enum": []}`, expect: []types.OptionEnum{}},
		{
			name:  "object instead of list",
			input: `{"enum": {"value": "dev"}}`,
			err:   types.ErrUnsupportedType,
		},
		{name: "integer elements", input: `{"enum": [1, 2]}`, err: types.ErrUnsupportedType},
	}, func(input []byte) ([]types.OptionEnum, error) {
		var data struct {
			Enum types.OptionEnumArray `json:"enum"`
		}
		err := json.Unmarshal(input, &data)
		return []types.OptionEnum(data.Enum), err
	})
}
