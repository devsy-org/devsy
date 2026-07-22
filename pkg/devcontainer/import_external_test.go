package devcontainer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
}

func importedProfilePath(ws string) string {
	return filepath.Join(ws, filepath.FromSlash(importedProfileParent), importedProfileName)
}

func TestImportExternalDevContainer_SelfContainedFolder(t *testing.T) {
	// A dedicated .devcontainer/ folder with a sibling Dockerfile.
	external := filepath.Join(t.TempDir(), ".devcontainer")
	writeFile(t, filepath.Join(external, "devcontainer.json"),
		`{"name":"ext","build":{"dockerfile":"Dockerfile"}}`)
	writeFile(t, filepath.Join(external, "Dockerfile"), "FROM alpine\n")

	ws := t.TempDir()
	r := &runner{localWorkspaceFolder: ws}

	cfg, err := r.importExternalDevContainer(filepath.Join(external, "devcontainer.json"))
	require.NoError(t, err)

	imported := importedProfilePath(ws)
	assert.FileExists(t, filepath.Join(imported, "devcontainer.json"))
	assert.FileExists(t, filepath.Join(imported, "Dockerfile"))

	assert.Equal(t, filepath.Join(imported, "devcontainer.json"), cfg.Origin)
	assert.Equal(t, "Dockerfile", cfg.GetDockerfile())
	dockerfile := filepath.Join(filepath.Dir(cfg.Origin), cfg.GetDockerfile())
	assert.FileExists(t, dockerfile)
}

func TestImportExternalDevContainer_ProjectRootCopiesOnlyConfig(t *testing.T) {
	// A config that lives at a project root (parent is NOT .devcontainer) must
	// copy only the config file — never the surrounding tree (secrets, .git,
	// node_modules, ...).
	external := t.TempDir()
	writeFile(t, filepath.Join(external, "devcontainer.json"), `{"image":"alpine"}`)
	writeFile(t, filepath.Join(external, "secret.env"), "TOKEN=shh")
	writeFile(t, filepath.Join(external, ".git", "config"), "[core]")

	ws := t.TempDir()
	r := &runner{localWorkspaceFolder: ws}

	_, err := r.importExternalDevContainer(filepath.Join(external, "devcontainer.json"))
	require.NoError(t, err)

	imported := importedProfilePath(ws)
	assert.FileExists(t, filepath.Join(imported, "devcontainer.json"))
	assert.NoFileExists(t, filepath.Join(imported, "secret.env"),
		"must not copy sibling files from a non-.devcontainer parent")
	assert.NoDirExists(t, filepath.Join(imported, ".git"),
		"must not copy sibling dirs from a non-.devcontainer parent")
}

func TestImportExternalDevContainer_ProfileFolderIsSelfContained(t *testing.T) {
	// A named profile (.devcontainer/<name>/devcontainer.json) is self-contained
	// and its siblings travel with it.
	external := filepath.Join(t.TempDir(), ".devcontainer", "backend")
	writeFile(t, filepath.Join(external, "devcontainer.json"),
		`{"build":{"dockerfile":"Dockerfile"}}`)
	writeFile(t, filepath.Join(external, "Dockerfile"), "FROM alpine\n")

	ws := t.TempDir()
	r := &runner{localWorkspaceFolder: ws}

	_, err := r.importExternalDevContainer(filepath.Join(external, "devcontainer.json"))
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(importedProfilePath(ws), "Dockerfile"))
}

func TestImportExternalDevContainer_BareFile(t *testing.T) {
	external := t.TempDir()
	writeFile(t, filepath.Join(external, "devcontainer.json"), `{"image":"alpine"}`)

	ws := t.TempDir()
	r := &runner{localWorkspaceFolder: ws}

	cfg, err := r.importExternalDevContainer(filepath.Join(external, "devcontainer.json"))
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(importedProfilePath(ws), "devcontainer.json"))
	assert.Equal(t, "alpine", cfg.Image)
}

func TestImportExternalDevContainer_WritesMarker(t *testing.T) {
	external := t.TempDir()
	writeFile(t, filepath.Join(external, "devcontainer.json"), `{"image":"alpine"}`)

	ws := t.TempDir()
	r := &runner{localWorkspaceFolder: ws}

	_, err := r.importExternalDevContainer(filepath.Join(external, "devcontainer.json"))
	require.NoError(t, err)
	assert.True(t, isImportedProfileDir(importedProfilePath(ws)),
		"imported profile must carry the marker file")
}

func TestImportExternalDevContainer_CollisionGetsSuffix(t *testing.T) {
	external := t.TempDir()
	writeFile(t, filepath.Join(external, "devcontainer.json"), `{"image":"alpine"}`)

	ws := t.TempDir()
	writeFile(t, filepath.Join(importedProfilePath(ws), "devcontainer.json"),
		`{"image":"project-owned"}`)
	r := &runner{localWorkspaceFolder: ws}

	cfg, err := r.importExternalDevContainer(filepath.Join(external, "devcontainer.json"))
	require.NoError(t, err)

	assert.Equal(t, "alpine", cfg.Image)
	base := filepath.Base(filepath.Dir(cfg.Origin))
	assert.True(t, strings.HasPrefix(base, importedProfileName+"_"),
		"expected a suffixed dir, got %q", base)
}

func TestImportExternalDevContainer_ReusesOwnProfile(t *testing.T) {
	external := t.TempDir()
	writeFile(t, filepath.Join(external, "devcontainer.json"), `{"image":"alpine"}`)

	ws := t.TempDir()
	r := &runner{localWorkspaceFolder: ws}

	first, err := r.importExternalDevContainer(filepath.Join(external, "devcontainer.json"))
	require.NoError(t, err)
	second, err := r.importExternalDevContainer(filepath.Join(external, "devcontainer.json"))
	require.NoError(t, err)
	assert.Equal(t, first.Origin, second.Origin)
}

func TestImportExternalDevContainer_AddsGitExclude(t *testing.T) {
	external := t.TempDir()
	writeFile(t, filepath.Join(external, "devcontainer.json"), `{"image":"alpine"}`)

	ws := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(ws, ".git", "info"), 0o750))
	r := &runner{localWorkspaceFolder: ws}

	_, err := r.importExternalDevContainer(filepath.Join(external, "devcontainer.json"))
	require.NoError(t, err)

	// #nosec G304 -- test path
	exclude, err := os.ReadFile(filepath.Join(ws, ".git", "info", "exclude"))
	require.NoError(t, err)
	assert.Contains(t, string(exclude), importedProfileParent+"/"+importedProfileName)
}

func TestImportExternalDevContainer_NonGitRepoSkipsExclude(t *testing.T) {
	external := t.TempDir()
	writeFile(t, filepath.Join(external, "devcontainer.json"), `{"image":"alpine"}`)

	ws := t.TempDir() // no .git
	r := &runner{localWorkspaceFolder: ws}

	_, err := r.importExternalDevContainer(filepath.Join(external, "devcontainer.json"))
	require.NoError(t, err, "import must succeed even outside a git repo")
}

func TestCleanupImportedDevContainers(t *testing.T) {
	external := t.TempDir()
	writeFile(t, filepath.Join(external, "devcontainer.json"), `{"image":"alpine"}`)

	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, ".devcontainer", "app", "devcontainer.json"), `{}`)

	r := &runner{localWorkspaceFolder: ws}
	_, err := r.importExternalDevContainer(filepath.Join(external, "devcontainer.json"))
	require.NoError(t, err)
	require.DirExists(t, importedProfilePath(ws))

	require.NoError(t, CleanupImportedDevContainers(ws))
	assert.NoDirExists(t, importedProfilePath(ws), "imported profile must be removed")
	assert.DirExists(t, filepath.Join(ws, ".devcontainer", "app"),
		"project-owned profile must be preserved")
}

func TestCleanupImportedDevContainers_NoDevcontainerDir(t *testing.T) {
	assert.NoError(t, CleanupImportedDevContainers(t.TempDir()))
}
