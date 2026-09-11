package git

import (
	"context"
	"testing"

	"gotest.tools/assert"
)

const testSubPath = "apps/foo"

func TestInspectionReadFileUsesSubPath(t *testing.T) {
	runner := &fakeRunner{stdout: []byte("secret-contents")}
	repo := At("/tmp/repo", WithRunner(runner))
	inspection := &Inspection{repo: repo, rev: inspectionHeadRev, subPath: testSubPath}

	out, err := inspection.ReadFile(context.Background(), ".devcontainer/devcontainer.json")
	assert.NilError(t, err)
	assert.Equal(t, string(out), "secret-contents")

	wantObject := inspectionHeadRev + ":" + testSubPath + "/.devcontainer/devcontainer.json"
	// cat-file existence check, then show; both must target the subpath.
	assert.Equal(t, len(runner.calls), 2)
	assert.Equal(t, runner.calls[0].Args[len(runner.calls[0].Args)-1], wantObject)
	assert.Equal(t, runner.calls[1].Args[len(runner.calls[1].Args)-1], wantObject)
}

func TestInspectionReadFileWithoutSubPathUsesRepoRoot(t *testing.T) {
	runner := &fakeRunner{stdout: []byte("secret-contents")}
	repo := At("/tmp/repo", WithRunner(runner))
	inspection := &Inspection{repo: repo, rev: inspectionHeadRev}

	_, err := inspection.ReadFile(context.Background(), ".devcontainer/devcontainer.json")
	assert.NilError(t, err)
	wantObject := inspectionHeadRev + ":.devcontainer/devcontainer.json"
	assert.Equal(t, runner.calls[0].Args[len(runner.calls[0].Args)-1], wantObject)
}

func TestCleanInspectionSubPath(t *testing.T) {
	for _, tc := range []struct {
		name    string
		value   string
		want    string
		wantErr bool
	}{
		{name: "empty", value: "", want: ""},
		{name: "dot", value: ".", want: ""},
		{name: "simple", value: testSubPath, want: testSubPath},
		{name: "trailing slash", value: testSubPath + "/", want: testSubPath},
		{name: "leading slash stripped", value: "/apps/bar", want: "apps/bar"},
		{name: "parent escape rejected", value: "../bar", wantErr: true},
		{name: "parent only rejected", value: "..", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := cleanInspectionSubPath(tc.value)
			if tc.wantErr {
				assert.Assert(t, err != nil)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, got, tc.want)
		})
	}
}

func TestInspectionReadFileRejectsPathEscape(t *testing.T) {
	runner := &fakeRunner{stdout: []byte("secret-contents")}
	repo := At("/tmp/repo", WithRunner(runner))
	inspection := &Inspection{repo: repo, rev: inspectionHeadRev, subPath: testSubPath}

	_, err := inspection.ReadFile(context.Background(), "../../etc/passwd")
	assert.Assert(t, err != nil)
	assert.Equal(t, len(runner.calls), 0)
}

func TestInspectionReadFileRejectsAbsolutePath(t *testing.T) {
	runner := &fakeRunner{stdout: []byte("secret-contents")}
	repo := At("/tmp/repo", WithRunner(runner))
	inspection := &Inspection{repo: repo, rev: inspectionHeadRev, subPath: testSubPath}

	for _, bad := range []string{
		"/secrets.enc.yaml",
		"/etc/passwd",
		`C:\secrets.enc.yaml`,
		`\\server\share\secrets.enc.yaml`,
	} {
		_, err := inspection.ReadFile(context.Background(), bad)
		assert.Assert(t, err != nil, bad)
	}
}

func TestInspectionReadDevContainerConfig_RootConfig(t *testing.T) {
	runner := &fakeRunner{stdout: []byte(`{"customizations":{"devsy":{}}}`)}
	repo := At("/tmp/repo", WithRunner(runner))
	inspection := &Inspection{repo: repo, rev: inspectionHeadRev}

	data, pathFound, err := inspection.ReadDevContainerConfig(context.Background(), "", "")
	assert.NilError(t, err)
	assert.Equal(t, pathFound, ".devcontainer/devcontainer.json")
	assert.Assert(t, len(data) > 0)
}

func TestInspectionReadDevContainerConfig_ExplicitPath(t *testing.T) {
	runner := &fakeRunner{stdout: []byte(`{"customizations":{"devsy":{}}}`)}
	repo := At("/tmp/repo", WithRunner(runner))
	inspection := &Inspection{repo: repo, rev: inspectionHeadRev}

	data, pathFound, err := inspection.ReadDevContainerConfig(
		context.Background(),
		"custom/devcontainer.json",
		"",
	)
	assert.NilError(t, err)
	assert.Equal(t, pathFound, "custom/devcontainer.json")
	assert.Assert(t, len(data) > 0)
}

func TestInspectionReadDevContainerConfig_DevContainerID(t *testing.T) {
	runner := &fakeRunner{stdout: []byte(`{"customizations":{"devsy":{}}}`)}
	repo := At("/tmp/repo", WithRunner(runner))
	inspection := &Inspection{repo: repo, rev: inspectionHeadRev}

	data, pathFound, err := inspection.ReadDevContainerConfig(
		context.Background(),
		"",
		"my-profile",
	)
	assert.NilError(t, err)
	assert.Equal(t, pathFound, ".devcontainer/my-profile/devcontainer.json")
	assert.Assert(t, len(data) > 0)
}
