package devcontainer

import (
	"fmt"
	"path/filepath"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
)

type devContainerSelectionDrift struct {
	pinnedPath     string
	discoveredPath string
}

func (r *runner) compatibilitySelectionWarning(
	selection devContainerSelection,
) string {
	drift, err := r.compatibilitySelectionDrift(selection)
	if err != nil {
		log.Debugf("could not check devcontainer selection drift: %v", err)
		return ""
	}
	if drift == nil {
		return ""
	}

	return fmt.Sprintf(
		"This existing workspace is continuing to use %q, its previously resolved devcontainer configuration. Current project discovery would select %q. Devsy does not switch an existing workspace implicitly; run workspace up with --devcontainer %s to adopt it explicitly.",
		drift.pinnedPath,
		drift.discoveredPath,
		drift.discoveredPath,
	)
}

func (r *runner) compatibilitySelectionDrift(
	selection devContainerSelection,
) (*devContainerSelectionDrift, error) {
	if selection.origin != selectionLastResolvedCompatibility || selection.path == "" {
		return nil, nil
	}

	folder := r.workspaceFolder()
	discoveredPath, err := config.DiscoverDevContainerPath(folder)
	if err != nil || discoveredPath == "" {
		return nil, err
	}

	pinnedPath, err := workspaceRelativePath(folder, selection.path)
	if err != nil {
		return nil, err
	}
	discoveredRelativePath, err := workspaceRelativePath(folder, discoveredPath)
	if err != nil {
		return nil, err
	}
	if pinnedPath == discoveredRelativePath {
		return nil, nil
	}

	return &devContainerSelectionDrift{
		pinnedPath:     pinnedPath,
		discoveredPath: discoveredRelativePath,
	}, nil
}

func workspaceRelativePath(folder, value string) (string, error) {
	path := filepath.FromSlash(value)
	if !filepath.IsAbs(path) {
		path = filepath.Join(folder, path)
	}
	relativePath, err := filepath.Rel(folder, path)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(filepath.Clean(relativePath)), nil
}
