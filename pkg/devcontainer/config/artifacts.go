package config

import (
	"os"
	"path/filepath"
)

// buildArtifactNames are the workspace-relative names devsy writes into the
// build context for its own bookkeeping (feature scaffolding, generated
// Dockerfiles, etc.). They are transient build-time artifacts, never workspace
// content, and never needed at runtime.
//
// This is the single source of truth: anything that copies, hashes, or cleans
// the workspace/build-context tree must consult these helpers rather than
// hard-coding names, so a new artifact only needs to be added here.
var buildArtifactNames = []string{
	DevsyContextFeatureFolder,
}

// BuildArtifactExcludes returns the artifact names to exclude when copying or
// hashing a tree (e.g. workspace-volume seeding, prebuild-hash computation).
// Names are relative to the tree root; callers apply them per their own
// matching syntax (tar --exclude, dockerignore, etc.).
func BuildArtifactExcludes() []string {
	return append([]string(nil), buildArtifactNames...)
}

// RemoveBuildArtifacts deletes devsy's build artifacts from contextPath. It is
// best-effort and safe to call when they are absent.
func RemoveBuildArtifacts(contextPath string) {
	for _, name := range buildArtifactNames {
		_ = os.RemoveAll(filepath.Join(contextPath, name))
	}
}
