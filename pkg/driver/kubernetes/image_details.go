package kubernetes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/metadata"
	"github.com/devsy-org/devsy/pkg/log"
)

// ImageDetails reads the image config from the file set in the IMAGE_CONFIG_FILE
// option. It returns nil when the option is empty, so callers fall back to
// reading the config from the registry.
func (k *KubernetesDriver) ImageDetails(imageName string) (*config.ImageDetails, error) {
	if k.options.ImageConfigFile == "" {
		return nil, nil
	}

	// Without ARCHITECTURE, Devsy lists the cluster's nodes to detect it,
	// which needs cluster-scoped permissions this option is meant to avoid.
	if k.options.Architecture == "" {
		return nil, fmt.Errorf("IMAGE_CONFIG_FILE requires the ARCHITECTURE option to be set")
	}

	log.Debugf("Reading image config for %q from %q", imageName, k.options.ImageConfigFile)
	details, err := loadImageDetails(k.options.ImageConfigFile, imageName)
	if err != nil {
		return nil, err
	}

	if details.Architecture != "" && details.Architecture != k.options.Architecture {
		return nil, fmt.Errorf(
			"image config file %q is for architecture %q, but ARCHITECTURE is %q",
			k.options.ImageConfigFile, details.Architecture, k.options.Architecture,
		)
	}

	return details, nil
}

// loadImageDetails parses an image config file. It accepts the JSON of
// config.ImageDetails, an OCI image config (e.g. from `crane config`) or a
// single `docker image inspect` object, since field names match
// case-insensitively and unknown fields are ignored.
func loadImageDetails(path, imageName string) (*config.ImageDetails, error) {
	// #nosec G304 -- path comes from the operator-controlled IMAGE_CONFIG_FILE provider option
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read image config file %q: %w", path, err)
	}

	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' {
		return nil, fmt.Errorf("image config file %q must contain a JSON object", path)
	}

	details := &config.ImageDetails{}
	if err := json.Unmarshal(data, details); err != nil {
		return nil, fmt.Errorf("parse image config file %q: %w", path, err)
	}

	// An image may legitimately have an empty config, so check that the
	// config object is present rather than that any of its fields are set.
	var probe struct {
		Config json.RawMessage `json:"config"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("parse image config file %q: %w", path, err)
	}
	if len(probe.Config) == 0 || probe.Config[0] != '{' {
		return nil, fmt.Errorf("image config file %q has no config object", path)
	}

	if err := validateImageMetadata(
		details.Config.Labels[metadata.ImageMetadataLabel],
	); err != nil {
		return nil, fmt.Errorf("image config file %q: %w", path, err)
	}

	if details.ID == "" {
		details.ID = imageName
	}

	return details, nil
}

// validateImageMetadata checks that the devcontainer.metadata label is a JSON
// object or an array of objects. Metadata read from the registry is ignored
// with a warning when malformed, but a file the operator supplied should fail
// instead of silently dropping its lifecycle commands and settings.
func validateImageMetadata(raw string) error {
	if raw == "" {
		return nil
	}

	var entries []*config.ImageMetadata
	if err := json.Unmarshal([]byte(raw), &entries); err == nil {
		if entries == nil {
			return fmt.Errorf("%s label is null", metadata.ImageMetadataLabel)
		}
		for _, entry := range entries {
			if entry == nil {
				return fmt.Errorf("%s label contains a null entry", metadata.ImageMetadataLabel)
			}
		}
		return nil
	}

	single := &config.ImageMetadata{}
	if raw[0] != '{' {
		return fmt.Errorf("%s label must be a JSON object or array", metadata.ImageMetadataLabel)
	}
	if err := json.Unmarshal([]byte(raw), single); err != nil {
		return fmt.Errorf("parse %s label: %w", metadata.ImageMetadataLabel, err)
	}
	return nil
}
