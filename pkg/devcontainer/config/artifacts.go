package config

import (
	"os"
	"path/filepath"
)

// buildArtifactNames is the single source of truth for the transient files
// devsy writes into the build context.
//
// Invariant: any operation that copies, streams, or hashes a workspace tree
// must exclude these via BuildArtifactExcludes. That exclusion — not the
// best-effort RemoveBuildArtifacts cleanup — is what keeps artifacts out of a
// user's workspace, so correctness never depends on cleanup timing.
var buildArtifactNames = []string{
	DevsyContextFeatureFolder,
}

// BuildArtifactExcludes returns the artifact names, relative to the tree root,
// for callers to exclude when copying, streaming, or hashing the tree.
func BuildArtifactExcludes() []string {
	return append([]string(nil), buildArtifactNames...)
}

// RemoveBuildArtifacts deletes devsy's build artifacts from contextPath.
// Best-effort tidiness only; see the invariant on buildArtifactNames.
func RemoveBuildArtifacts(contextPath string) {
	for _, name := range buildArtifactNames {
		_ = os.RemoveAll(filepath.Join(contextPath, name))
	}
}
