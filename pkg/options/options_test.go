package options

import (
	"os"
	"testing"

	"gotest.tools/assert"
)

type assignmentTestCase struct {
	Name string

	Names                     []string
	Assignments               []string
	EnvironmentVariablePrefix string
	NotInEnvironment          []string
	Environment               map[string]string

	ExpectedAssignments []string
}

const (
	testEnvHostName   = "HOST"
	testEnvHostAssign = "HOST=box"
	testEnvSSHPrefix  = "DEVSY_PROVIDER_SSH_"
	testEnvSSHHostVar = "DEVSY_PROVIDER_SSH_HOST"
)

var inheritFromEnvironmentTestCases = []assignmentTestCase{
	{
		Name: "assigned, not in the environment",
		Names: []string{
			testEnvHostName,
		},
		Assignments: []string{
			testEnvHostAssign,
		},
		EnvironmentVariablePrefix: testEnvSSHPrefix,
		NotInEnvironment: []string{
			testEnvSSHHostVar,
		},
		Environment: map[string]string{},
		ExpectedAssignments: []string{
			testEnvHostAssign,
		},
	},
	{
		Name: "not assigned, not in the environment",
		Names: []string{
			testEnvHostName,
		},
		Assignments:               []string{},
		EnvironmentVariablePrefix: testEnvSSHPrefix,
		NotInEnvironment: []string{
			testEnvSSHHostVar,
		},
		Environment:         map[string]string{},
		ExpectedAssignments: []string{},
	},
	{
		Name: "assigned, in the environment",
		Names: []string{
			testEnvHostName,
		},
		Assignments: []string{
			testEnvHostAssign,
		},
		EnvironmentVariablePrefix: testEnvSSHPrefix,
		NotInEnvironment:          []string{},
		Environment: map[string]string{
			testEnvSSHHostVar: "another-box",
		},
		ExpectedAssignments: []string{
			testEnvHostAssign,
		},
	},
	{
		Name: "not assigned, in the environment",
		Names: []string{
			testEnvHostName,
		},
		Assignments:               []string{},
		EnvironmentVariablePrefix: testEnvSSHPrefix,
		NotInEnvironment:          []string{},
		Environment: map[string]string{
			testEnvSSHHostVar: "another-box",
		},
		ExpectedAssignments: []string{
			"HOST=another-box",
		},
	},
}

func TestInheritFromEnvironment(t *testing.T) {
	for _, testCase := range inheritFromEnvironmentTestCases {
		t.Run(testCase.Name, func(t *testing.T) {
			runInheritFromEnvironmentTestCase(t, testCase)
		})
	}
}

func runInheritFromEnvironmentTestCase(t *testing.T, testCase assignmentTestCase) {
	for _, k := range testCase.NotInEnvironment {
		err := os.Unsetenv(k)
		if err != nil {
			t.Fatalf("unexpected error %v in %s", err, testCase.Name)
		}
	}
	for k, v := range testCase.Environment {
		err := os.Setenv(k, v)
		if err != nil {
			t.Fatalf("unexpected error %v in %s", err, testCase.Name)
		}
	}

	result := InheritFromEnvironment(
		testCase.Assignments,
		testCase.Names,
		testCase.EnvironmentVariablePrefix,
	)

	assert.DeepEqual(t, result, testCase.ExpectedAssignments)
}
