package config

import (
	"os"
	"path/filepath"
)

// buildArtifactNames is the single source of truth for the transient files
// devsy writes into the build context. Consumers that copy, hash, or clean the
// tree consult the helpers below rather than hard-coding names.
var buildArtifactNames = []string{
	DevsyContextFeatureFolder,
}

// BuildArtifactExcludes returns the artifact names, relative to the tree root,
// for callers to exclude when copying or hashing (tar, dockerignore, etc.).
func BuildArtifactExcludes() []string {
	return append([]string(nil), buildArtifactNames...)
}

// RemoveBuildArtifacts deletes devsy's build artifacts from contextPath.
// Best-effort and safe to call when they are absent.
func RemoveBuildArtifacts(contextPath string) {
	for _, name := range buildArtifactNames {
		_ = os.RemoveAll(filepath.Join(contextPath, name))
	}
}
