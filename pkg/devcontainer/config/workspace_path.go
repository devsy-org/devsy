package config

import (
	"fmt"
	"path/filepath"
)

// CanonicalWorkspacePath resolves the authorized root, preserving symlinks below it for validation.
func CanonicalWorkspacePath(workspace, candidate string) (string, string, error) {
	root, err := filepath.Abs(workspace)
	if err != nil {
		return "", "", err
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", err
	}
	absolute, err := filepath.Abs(candidate)
	if err != nil {
		return "", "", err
	}
	for _, spelling := range []string{root, canonicalRoot} {
		relative, relErr := filepath.Rel(spelling, absolute)
		if relErr == nil && filepath.IsLocal(relative) {
			return canonicalRoot, filepath.Join(canonicalRoot, relative), nil
		}
	}
	return "", "", fmt.Errorf("path %q is outside workspace %q", candidate, workspace)
}
