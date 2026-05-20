package workspace

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	testLocalNewWS = "/home/user/new-ws"
	testLocalOldWS = "/home/user/old-ws"
)

func TestNewPathReplacer_DefaultWorkspaceDir(t *testing.T) {
	r := newPathReplacer(testLocalOldWS, "old-ws", "new-ws")

	expected := [][2]string{
		{testLocalOldWS, testLocalNewWS},
	}

	assert.NotNil(t, r)
	assert.Equal(t, expected, r.pairs)
	assert.False(t, r.changed)
}

func TestNewPathReplacer_NonDefaultWorkspaceDir(t *testing.T) {
	r := newPathReplacer("/mnt/data/project", "project", "renamed")

	expected := [][2]string{
		{"/mnt/data/project", "/mnt/data/renamed"},
	}

	assert.Equal(t, expected, r.pairs)
	assert.False(t, r.changed)
}

func TestNewPathReplacer_NestedWorkspacePath(t *testing.T) {
	r := newPathReplacer(
		"/home/user/workspace/dev/projects/my-app",
		"my-app",
		"my-app-v2",
	)

	expected := [][2]string{
		{
			"/home/user/workspace/dev/projects/my-app",
			"/home/user/workspace/dev/projects/my-app-v2",
		},
	}

	assert.Equal(t, expected, r.pairs)
}

func TestNewPathReplacer_EmptyLocalFolder(t *testing.T) {
	r := newPathReplacer("", "old-ws", "new-ws")

	assert.Nil(t, r.pairs)
}

func TestNewPathReplacer_SpecialCharacters(t *testing.T) {
	r := newPathReplacer(
		"/home/user/my-app_v1.0",
		"my-app_v1.0",
		"my-app_v2.0",
	)

	expected := [][2]string{
		{"/home/user/my-app_v1.0", "/home/user/my-app_v2.0"},
	}

	assert.Equal(t, expected, r.pairs)
}

func TestPathReplacer_Replace_BasicReplacement(t *testing.T) {
	r := &pathReplacer{
		pairs: [][2]string{
			{testLocalOldWS, testLocalNewWS},
		},
	}

	output := r.replace("/home/user/old-ws/src/main.go")

	assert.Equal(t, "/home/user/new-ws/src/main.go", output)
	assert.True(t, r.changed)
}

func TestPathReplacer_Replace_NoMatch(t *testing.T) {
	r := &pathReplacer{
		pairs: [][2]string{
			{testLocalOldWS, testLocalNewWS},
		},
	}

	output := r.replace("/home/user/other-ws/src/main.go")

	assert.Equal(t, "/home/user/other-ws/src/main.go", output)
	assert.False(t, r.changed)
}

func TestPathReplacer_Replace_MultipleReplacements(t *testing.T) {
	r := &pathReplacer{
		pairs: [][2]string{
			{testLocalOldWS, testLocalNewWS},
		},
	}

	input := "source=/home/user/old-ws,target=/workspaces/old-ws,type=bind"
	expected := "source=/home/user/new-ws,target=/workspaces/old-ws,type=bind"

	output := r.replace(input)

	assert.Equal(t, expected, output)
	assert.True(t, r.changed)
}

func TestPathReplacer_Replace_EmptyString(t *testing.T) {
	r := &pathReplacer{
		pairs: [][2]string{
			{testLocalOldWS, testLocalNewWS},
		},
	}

	output := r.replace("")

	assert.Equal(t, "", output)
	assert.False(t, r.changed)
}

func TestPathReplacer_Replace_MultipleOccurrences(t *testing.T) {
	r := &pathReplacer{
		pairs: [][2]string{
			{"/home/user/app", "/home/user/app-new"},
		},
	}

	input := "/home/user/app/go.mod /home/user/app/go.sum"
	expected := "/home/user/app-new/go.mod /home/user/app-new/go.sum"

	output := r.replace(input)

	assert.Equal(t, expected, output)
	assert.True(t, r.changed)
}

func TestPathReplacer_Replace_NoPairs(t *testing.T) {
	r := &pathReplacer{
		pairs: nil,
	}

	output := r.replace("/home/user/old-ws/src/main.go")

	assert.Equal(t, "/home/user/old-ws/src/main.go", output)
	assert.False(t, r.changed)
}

func TestPathReplacer_Replace_WorkspaceMount(t *testing.T) {
	r := &pathReplacer{
		pairs: [][2]string{
			{testLocalOldWS, testLocalNewWS},
		},
	}

	input := "type=bind,source=/home/user/old-ws,target=/workspaces/old-ws"
	expected := "type=bind,source=/home/user/new-ws,target=/workspaces/old-ws"

	output := r.replace(input)

	assert.Equal(t, expected, output)
	assert.True(t, r.changed)
}

func TestPathReplacer_ReplaceMultipleCalls(t *testing.T) {
	r := &pathReplacer{
		pairs: [][2]string{
			{"/home/user/old", "/home/user/new"},
		},
	}

	r.replace("/home/user/other")
	assert.False(t, r.changed)

	r.replace("/home/user/old")
	assert.True(t, r.changed)

	r.replace("/home/user/other")
	assert.True(t, r.changed, "changed flag should remain true once set")
}

func TestPathReplacer_ReplacePairOrder(t *testing.T) {
	r := &pathReplacer{
		pairs: [][2]string{
			{"/home/old", "/home/new"},
			{"/mnt/old", "/mnt/new"},
		},
	}

	input := "/home/old /mnt/old"
	expected := "/home/new /mnt/new"

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
				{"/home/user/old", "/home/user/new"},
			},
			input:    "/home/user/old",
			expected: "/home/user/new",
		},
		{
			name: "trailing slash in input only",
			pairs: [][2]string{
				{"/home/user/old", "/home/user/new"},
			},
			input:    "/home/user/old/",
			expected: "/home/user/new/",
		},
		{
			name: "subpath match",
			pairs: [][2]string{
				{"/home/user/old", "/home/user/new"},
			},
			input:    "/home/user/old/subdir/file.txt",
			expected: "/home/user/new/subdir/file.txt",
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
