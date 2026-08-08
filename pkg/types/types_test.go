package types_test

import (
	"encoding/json"
	"testing"

	"github.com/devsy-org/devsy/pkg/types"
	"gotest.tools/assert"
)

type lifecycleHookUnmarshalInput struct {
	Input types.LifecycleHook `json:"input,omitempty"`
}

type lifecycleHookUnmarshalCase struct {
	Name   string
	Input  string
	Expect lifecycleHookUnmarshalInput
}

func lifecycleHookUnmarshalTestCases() []lifecycleHookUnmarshalCase {
	return []lifecycleHookUnmarshalCase{
		{
			Name:  "string",
			Input: `{"input": "some-string"}`,
			Expect: lifecycleHookUnmarshalInput{
				Input: types.LifecycleHook{
					"": []string{"some-string"},
				},
			},
		},
		{
			Name:  "array of strings",
			Input: `{"input": ["string1", "string2"]}`,
			Expect: lifecycleHookUnmarshalInput{
				Input: types.LifecycleHook{
					"": []string{
						"string1",
						"string2",
					},
				},
			},
		},
		{
			Name:  "object of strings",
			Input: `{"input": {"key1": "value1", "key2": "value2"}}`,
			Expect: lifecycleHookUnmarshalInput{
				Input: types.LifecycleHook{
					"key1": []string{
						"value1",
					},
					"key2": []string{
						"value2",
					},
				},
			},
		},
		{
			Name:  "object of array of strings",
			Input: `{"input": {"key1": ["value1","value2"], "key2": ["value3","value4"]}}`,
			Expect: lifecycleHookUnmarshalInput{
				Input: types.LifecycleHook{
					"key1": []string{
						"value1",
						"value2",
					},
					"key2": []string{
						"value3",
						"value4",
					},
				},
			},
		},
	}
}

func TestLifecycleHookUnmarshalJSON(t *testing.T) {
	for _, testCase := range lifecycleHookUnmarshalTestCases() {
		t.Run(testCase.Name, func(t *testing.T) {
			var data lifecycleHookUnmarshalInput

			err := json.Unmarshal([]byte(testCase.Input), &data)
			assert.NilError(t, err, testCase.Name)

			assert.DeepEqual(t, testCase.Expect, data)
		})
	}
}
