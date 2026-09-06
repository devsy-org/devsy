package git

import (
	"context"
	"testing"

	"gotest.tools/assert"
)

const testSubPath = "apps/foo"

// TestInspectionReadFileUsesSubPath verifies that ReadFile resolves paths
// relative to the selected @subpath: project root instead of the repository
// root, so repository-owned config (e.g. .devsy/config.yaml) and SOPS source
// files inside a subproject are discovered correctly.
func TestInspectionReadFileUsesSubPath(t *testing.T) {
	runner := &fakeRunner{stdout: []byte("secret-contents")}
	repo := At("/tmp/repo", WithRunner(runner))
	inspection := &Inspection{repo: repo, rev: inspectionHeadRev, subPath: testSubPath}

	out, err := inspection.ReadFile(context.Background(), ".devsy/config.yaml")
	assert.NilError(t, err)
	assert.Equal(t, string(out), "secret-contents")

	wantObject := inspectionHeadRev + ":" + testSubPath + "/.devsy/config.yaml"
	// cat-file existence check, then show; both must target the subpath.
	assert.Equal(t, len(runner.calls), 2)
	assert.Equal(t, runner.calls[0].Args[len(runner.calls[0].Args)-1], wantObject)
	assert.Equal(t, runner.calls[1].Args[len(runner.calls[1].Args)-1], wantObject)
}

func TestInspectionReadFileWithoutSubPathUsesRepoRoot(t *testing.T) {
	runner := &fakeRunner{stdout: []byte("secret-contents")}
	repo := At("/tmp/repo", WithRunner(runner))
	inspection := &Inspection{repo: repo, rev: inspectionHeadRev}

	_, err := inspection.ReadFile(context.Background(), ".devsy/config.yaml")
	assert.NilError(t, err)
	wantObject := inspectionHeadRev + ":.devsy/config.yaml"
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

// TestInspectionReadFileRejectsPathEscape is a regression test ensuring
// ReadFile itself rejects a path that would escape the repository root (or
// the selected subpath) once cleaned and joined, rather than relying solely
// on callers to pre-validate the path.
func TestInspectionReadFileRejectsPathEscape(t *testing.T) {
	runner := &fakeRunner{stdout: []byte("secret-contents")}
	repo := At("/tmp/repo", WithRunner(runner))
	inspection := &Inspection{repo: repo, rev: inspectionHeadRev, subPath: testSubPath}

	_, err := inspection.ReadFile(context.Background(), "../../etc/passwd")
	assert.Assert(t, err != nil)
	assert.Equal(t, len(runner.calls), 0)
}
