package config

import (
	"os"
	"path/filepath"
)

var buildArtifactNames = []string{
	DevsyContextFeatureFolder,
}

// BuildArtifactExcludes returns the build artifact names to exclude when
// copying, streaming, or hashing a workspace tree.
func BuildArtifactExcludes() []string {
	return append([]string(nil), buildArtifactNames...)
}

// RemoveBuildArtifacts deletes devsy's build artifacts from contextPath.
func RemoveBuildArtifacts(contextPath string) {
	for _, name := range buildArtifactNames {
		_ = os.RemoveAll(filepath.Join(contextPath, name))
	}
}
