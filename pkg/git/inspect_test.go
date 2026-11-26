package git

import (
	"context"
	"os"
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

const inspectTestRepository = "https://github.com/devsy-org/devsy.git"

func useDefaultRunner(t *testing.T, runner Runner) {
	t.Helper()
	previous := defaultRunner
	defaultRunner = runner
	t.Cleanup(func() { defaultRunner = previous })
}

func isolateInspectTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	t.Setenv("TMP", dir)
	t.Setenv("TEMP", dir)
	return dir
}

func tempEntryCount(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	assert.NilError(t, err)
	return len(entries)
}

func TestInspectRemoteRemovesTempCloneOnSetupFailure(t *testing.T) {
	tests := []struct {
		name     string
		info     GitInfo
		stdout   []byte
		errUntil int
	}{
		{
			name:     "clone fails",
			info:     GitInfo{Repository: inspectTestRepository},
			errUntil: 1,
		},
		{
			name: "pull request reference invalid",
			info: GitInfo{Repository: inspectTestRepository, PR: "not-a-pr"},
		},
		{
			name: "commit sha empty",
			info: GitInfo{Repository: inspectTestRepository},
		},
		{
			name:   "subpath escapes repository",
			info:   GitInfo{Repository: inspectTestRepository, SubPath: "../escape"},
			stdout: []byte("abc123\n"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := isolateInspectTempDir(t)
			useDefaultRunner(t, &fakeRunner{stdout: tt.stdout, errUntil: tt.errUntil})

			inspection, err := InspectRemote(context.Background(), &tt.info, nil)
			assert.Assert(t, err != nil)
			assert.Assert(t, inspection == nil)
			assert.Equal(t, tempEntryCount(t, tempDir), 0)
		})
	}
}

func TestInspectRemoteKeepsTempCloneUntilClose(t *testing.T) {
	tempDir := isolateInspectTempDir(t)
	useDefaultRunner(t, &fakeRunner{stdout: []byte("abc123\n")})

	inspection, err := InspectRemote(
		context.Background(),
		&GitInfo{Repository: inspectTestRepository, SubPath: testSubPath},
		nil,
	)
	assert.NilError(t, err)
	assert.Equal(t, inspection.Revision(), "abc123")
	assert.Equal(t, tempEntryCount(t, tempDir), 1)

	assert.NilError(t, inspection.Close())
	assert.Equal(t, tempEntryCount(t, tempDir), 0)
}
