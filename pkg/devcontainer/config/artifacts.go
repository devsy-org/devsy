package config

import (
	"os"
	"path/filepath"

	"github.com/devsy-org/devsy/pkg/log"
)

var buildArtifactNames = []string{
	DevsyContextFeatureFolder,
}

func BuildArtifactExcludes() []string {
	return append([]string(nil), buildArtifactNames...)
}

func RemoveBuildArtifacts(contextPath string) {
	for _, name := range buildArtifactNames {
		path := filepath.Join(contextPath, name)
		if err := os.RemoveAll(path); err != nil {
			log.Debugf("failed to remove build artifact %s: %v", path, err)
		}
	}
}
