package devcontainer

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
)

func loadDevContainerOverlay(
	ctx context.Context,
	overlayPath string,
) (*config.DevContainerConfig, error) {
	if overlayPath == "" {
		return nil, nil
	}
	overlay, err := config.ParseDevContainerJSONFile(ctx, overlayPath)
	if err != nil {
		return nil, fmt.Errorf("parse devcontainer overlay %q: %w", overlayPath, err)
	}
	return overlay, nil
}

// Only feature planning inputs are applied here. Runtime metadata retains its
// existing merge semantics after the build; the primary origin owns build assets.
func applyOverlayBuildInputs(base, overlay *config.DevContainerConfig) error {
	if overlay == nil {
		return nil
	}
	for id, value := range overlay.Features {
		rebased, err := rebaseOverlayLocalFeatureID(base.Origin, overlay.Origin, id)
		if err != nil {
			return err
		}
		if base.Features == nil {
			base.Features = make(map[string]any)
		}
		base.Features[rebased] = value
	}
	if overlay.OverrideFeatureInstallOrder != nil {
		order := make([]string, len(overlay.OverrideFeatureInstallOrder))
		for i, id := range overlay.OverrideFeatureInstallOrder {
			rebased, err := rebaseOverlayLocalFeatureID(base.Origin, overlay.Origin, id)
			if err != nil {
				return err
			}
			order[i] = rebased
		}
		base.OverrideFeatureInstallOrder = order
	}
	return nil
}

func rebaseOverlayLocalFeatureID(baseOrigin, overlayOrigin, featureID string) (string, error) {
	if !strings.HasPrefix(featureID, "./") && !strings.HasPrefix(featureID, "../") {
		return featureID, nil
	}
	featurePath := filepath.Join(filepath.Dir(overlayOrigin), filepath.FromSlash(featureID))
	relative, err := filepath.Rel(filepath.Dir(baseOrigin), featurePath)
	if err != nil {
		return "", fmt.Errorf(
			"rebase overlay feature %q from %q to %q: %w",
			featureID,
			overlayOrigin,
			baseOrigin,
			err,
		)
	}
	relative = filepath.ToSlash(relative)
	if !strings.HasPrefix(relative, "../") && !strings.HasPrefix(relative, "./") {
		if relative == "." {
			return "./", nil
		}
		relative = "./" + relative
	}
	return relative, nil
}

func overlayForParsedConfig(
	ctx context.Context,
	parsed *config.SubstitutedConfig,
	overlayPath string,
) (*config.DevContainerConfig, error) {
	if parsed != nil && parsed.Overlay != nil {
		return parsed.Overlay, nil
	}
	// Secondary callers may construct SubstitutedConfig without resolving an overlay.
	return loadDevContainerOverlay(ctx, overlayPath)
}
