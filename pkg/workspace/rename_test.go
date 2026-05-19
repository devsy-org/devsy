package workspace

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	testContainerNewWS = "/workspaces/new-ws"
	testLocalNewWS     = "/home/user/new-ws"
	testContainerNew   = "/workspaces/new"
	testContainerOldWS = "/workspaces/old-ws"
	testLocalOldWS     = "/home/user/old-ws"
	testContainerApp   = "/workspaces/app"
	testContainerOld   = "/workspaces/old"
)

func TestNewPathReplacer_DefaultWorkspaceDir(t *testing.T) {
	r := newPathReplacer(testContainerOldWS, testLocalOldWS, "old-ws", "new-ws")

	expected := [][2]string{
		{testContainerOldWS, testContainerNewWS},
		{testLocalOldWS, testLocalNewWS},
	}

	assert.NotNil(t, r)
	assert.Equal(t, expected, r.pairs)
	assert.False(t, r.changed)
}

func TestNewPathReplacer_NonDefaultWorkspaceDir(t *testing.T) {
	r := newPathReplacer("/home/user/project", "/mnt/data/project", "project", "renamed")

	expected := [][2]string{
		{"/home/user/project", "/home/user/renamed"},
		{"/mnt/data/project", "/mnt/data/renamed"},
	}

	assert.Equal(t, expected, r.pairs)
	assert.False(t, r.changed)
}

func TestNewPathReplacer_NestedWorkspacePath(t *testing.T) {
	r := newPathReplacer(
		"/workspace/dev/projects/my-app",
		"/home/user/workspace/dev/projects/my-app",
		"my-app",
		"my-app-v2",
	)

	expected := [][2]string{
		{"/workspace/dev/projects/my-app", "/workspace/dev/projects/my-app-v2"},
		{
			"/home/user/workspace/dev/projects/my-app",
			"/home/user/workspace/dev/projects/my-app-v2",
		},
	}

	assert.Equal(t, expected, r.pairs)
}

func TestNewPathReplacer_EmptyContainerFolder(t *testing.T) {
	r := newPathReplacer("", testLocalOldWS, "old-ws", "new-ws")

	expected := [][2]string{
		{testLocalOldWS, testLocalNewWS},
	}

	assert.Equal(t, expected, r.pairs)
}

func TestNewPathReplacer_EmptyLocalFolder(t *testing.T) {
	r := newPathReplacer(testContainerOldWS, "", "old-ws", "new-ws")

	expected := [][2]string{
		{testContainerOldWS, testContainerNewWS},
	}

	assert.Equal(t, expected, r.pairs)
}

func TestNewPathReplacer_BothEmpty(t *testing.T) {
	r := newPathReplacer("", "", "old-ws", "new-ws")

	assert.Nil(t, r.pairs)
}

func TestNewPathReplacer_SpecialCharacters(t *testing.T) {
	r := newPathReplacer(
		"/workspaces/my-app_v1.0",
		"/home/user/my-app_v1.0",
		"my-app_v1.0",
		"my-app_v2.0",
	)

	expected := [][2]string{
		{"/workspaces/my-app_v1.0", "/workspaces/my-app_v2.0"},
		{"/home/user/my-app_v1.0", "/home/user/my-app_v2.0"},
	}

	assert.Equal(t, expected, r.pairs)
}

func TestPathReplacer_Replace_BasicReplacement(t *testing.T) {
	r := &pathReplacer{
		pairs: [][2]string{
			{testContainerOldWS, testContainerNewWS},
		},
	}

	output := r.replace("/workspaces/old-ws/src/main.go")

	assert.Equal(t, "/workspaces/new-ws/src/main.go", output)
	assert.True(t, r.changed)
}

func TestPathReplacer_Replace_NoMatch(t *testing.T) {
	r := &pathReplacer{
		pairs: [][2]string{
			{testContainerOldWS, testContainerNewWS},
		},
	}

	output := r.replace("/workspaces/other-ws/src/main.go")

	assert.Equal(t, "/workspaces/other-ws/src/main.go", output)
	assert.False(t, r.changed)
}

func TestPathReplacer_Replace_MultipleReplacements(t *testing.T) {
	r := &pathReplacer{
		pairs: [][2]string{
			{testContainerOldWS, testContainerNewWS},
			{testLocalOldWS, testLocalNewWS},
		},
	}

	input := "source=/home/user/old-ws,target=/workspaces/old-ws,type=bind"
	expected := "source=/home/user/new-ws,target=/workspaces/new-ws,type=bind"

	output := r.replace(input)

	assert.Equal(t, expected, output)
	assert.True(t, r.changed)
}

func TestPathReplacer_Replace_EmptyString(t *testing.T) {
	r := &pathReplacer{
		pairs: [][2]string{
			{testContainerOldWS, testContainerNewWS},
		},
	}

	output := r.replace("")

	assert.Equal(t, "", output)
	assert.False(t, r.changed)
}

func TestPathReplacer_Replace_MultipleOccurrences(t *testing.T) {
	r := &pathReplacer{
		pairs: [][2]string{
			{testContainerApp, "/workspaces/app-new"},
		},
	}

	input := testContainerApp + "/go.mod " + testContainerApp + "/go.sum"
	expected := "/workspaces/app-new/go.mod /workspaces/app-new/go.sum"

	output := r.replace(input)

	assert.Equal(t, expected, output)
	assert.True(t, r.changed)
}

func TestPathReplacer_Replace_PartialMatch(t *testing.T) {
	r := &pathReplacer{
		pairs: [][2]string{
			{testContainerApp, "/workspaces/app-new"},
		},
	}

	input := "/workspaces/application/src"
	expected := "/workspaces/app-newlication/src"

	output := r.replace(input)

	assert.Equal(t, expected, output)
	assert.True(t, r.changed)
}

func TestPathReplacer_Replace_NoPairs(t *testing.T) {
	r := &pathReplacer{
		pairs: nil,
	}

	output := r.replace("/workspaces/old-ws/src/main.go")

	assert.Equal(t, "/workspaces/old-ws/src/main.go", output)
	assert.False(t, r.changed)
}

func TestPathReplacer_Replace_WorkspaceMount(t *testing.T) {
	r := &pathReplacer{
		pairs: [][2]string{
			{testContainerOldWS, testContainerNewWS},
			{testLocalOldWS, testLocalNewWS},
		},
	}

	input := "type=bind,source=/home/user/old-ws,target=/workspaces/old-ws"
	expected := "type=bind,source=/home/user/new-ws,target=/workspaces/new-ws"

	output := r.replace(input)

	assert.Equal(t, expected, output)
	assert.True(t, r.changed)
}

func TestPathReplacer_ReplaceMultipleCalls(t *testing.T) {
	r := &pathReplacer{
		pairs: [][2]string{
			{testContainerOld, testContainerNew},
		},
	}

	r.replace("/workspaces/other")
	assert.False(t, r.changed)

	r.replace(testContainerOld)
	assert.True(t, r.changed)

	r.replace("/workspaces/other")
	assert.True(t, r.changed, "changed flag should remain true once set")
}

func TestPathReplacer_ReplacePairOrder(t *testing.T) {
	r := &pathReplacer{
		pairs: [][2]string{
			{testContainerOld, testContainerNew},
			{"/home/old", "/home/new"},
			{"/mnt/old", "/mnt/new"},
		},
	}

	input := testContainerOld + " /home/old /mnt/old"
	expected := testContainerNew + " /home/new /mnt/new"

	output := r.replace(input)

	assert.Equal(t, expected, output)
	assert.True(t, r.changed)
}

func TestPathReplacer_ReplaceWithTrailingSlash(t *testing.T) {
	tests := []struct {
		name     string
		pairs    [][2]string
		input    string
		expected string
	}{
		{
			name: "no trailing slash in pair or input",
			pairs: [][2]string{
				{testContainerOld, testContainerNew},
			},
			input:    testContainerOld,
			expected: testContainerNew,
		},
		{
			name: "trailing slash in input only",
			pairs: [][2]string{
				{testContainerOld, testContainerNew},
			},
			input:    testContainerOld + "/",
			expected: testContainerNew + "/",
		},
		{
			name: "subpath match",
			pairs: [][2]string{
				{testContainerOld, testContainerNew},
			},
			input:    testContainerOld + "/subdir/file.txt",
			expected: testContainerNew + "/subdir/file.txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &pathReplacer{pairs: tt.pairs}
			output := r.replace(tt.input)
			assert.Equal(t, tt.expected, output)
		})
	}
}
