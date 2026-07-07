package config

import (
	"os"
	"path/filepath"

	"github.com/devsy-org/devsy/pkg/log"
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
		path := filepath.Join(contextPath, name)
		if err := os.RemoveAll(path); err != nil {
			log.Debugf("failed to remove build artifact %s: %v", path, err)
		}
	}
}
