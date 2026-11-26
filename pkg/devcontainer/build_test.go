package devcontainer

import (
	"testing"

	"gotest.tools/assert"
)

const (
	workspacesTest = "/workspaces/test"
)

func TestFallbackContainerContextAndDockerfile(t *testing.T) {
	testCases := []struct {
		name string

		localFolder  string
		remoteFolder string
		context      string
		dockerfile   string

		expectedContext    string
		expectedDockerfile string
	}{
		{
			name: "simple",

			localFolder:  "/my/local/folder",
			remoteFolder: workspacesTest,
			context:      "/my/local/folder/context",
			dockerfile:   "/my/local/folder/Dockerfile",

			expectedContext:    "/workspaces/test/context",
			expectedDockerfile: "/workspaces/test/Dockerfile",
		},
		{
			name: "windows",

			localFolder:  "C:/my/local/folder",
			remoteFolder: workspacesTest,
			context:      "C:/my/local/folder",
			dockerfile:   "C:/my/local/folder/Dockerfile",

			expectedContext:    workspacesTest,
			expectedDockerfile: "/workspaces/test/Dockerfile",
		},
	}

	for _, testCase := range testCases {
		outContext, outDockerfile := getContainerContextAndDockerfile(
			testCase.localFolder,
			testCase.remoteFolder,
			testCase.context,
			testCase.dockerfile,
		)
		assert.Equal(t, outContext, testCase.expectedContext, testCase.name)
		assert.Equal(t, outDockerfile, testCase.expectedDockerfile, testCase.name)
	}
}
