package feature

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
)

type localFeature struct {
	dir    string
	config *config.FeatureConfig
}

func discoverLocalFeatures(
	root, readErrPrefix string,
	onSkip func(name string, err error),
) ([]localFeature, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", readErrPrefix, err)
	}

	var features []localFeature
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		featureCfg, parseErr := config.ParseDevContainerFeature(filepath.Join(root, entry.Name()))
		if parseErr != nil {
			if onSkip != nil {
				onSkip(entry.Name(), parseErr)
			}
			continue
		}

		features = append(features, localFeature{
			dir:    entry.Name(),
			config: featureCfg,
		})
	}

	if len(features) == 0 {
		return nil, fmt.Errorf("no features found in %s", root)
	}
	return features, nil
}
