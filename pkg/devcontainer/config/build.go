package config

import (
	"fmt"
	"strings"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/dockerfile"
	"github.com/devsy-org/devsy/pkg/flags/names"
)

const (
	DockerfileDefaultTarget        = "dev_container_auto_added_stage_label"
	DevsyContextFeatureFolder      = pkgconfig.ConfigDirName + "-internal"
	DevsyDockerlessBuildInfoFolder = "/workspaces/.dockerless"
)

func GetDockerLabelForID(id string) []string {
	return []string{pkgconfig.DevcontainerIDLabel + "=" + id}
}

func GetIDLabels(id string, idLabels []string) []string {
	if len(idLabels) > 0 {
		return append([]string(nil), idLabels...)
	}
	return GetDockerLabelForID(id)
}

func ValidateIDLabels(labels []string) error {
	for _, label := range labels {
		k, _, ok := strings.Cut(label, "=")
		if !ok || k == "" {
			return fmt.Errorf(
				"invalid %s %q: must be in key=value format",
				names.Flag(names.IDLabel),
				label,
			)
		}
	}
	return nil
}

type BuildInfo struct {
	BuiltLocally  bool
	ImageName     string
	PrebuildHash  string
	RegistryCache string
	ImageDetails  *ImageDetails
	ImageMetadata *ImageMetadataConfig
	Dockerless    *BuildInfoDockerless
	Tags          []string
}

type BuildInfoDockerless struct {
	Context    string
	Dockerfile string

	BuildArgs map[string]string
	Target    string

	User string
}

type ImageBuildInfo struct {
	User     string
	Metadata *ImageMetadataConfig

	// Either on of these will be filled as will
	Dockerfile   *dockerfile.Dockerfile
	ImageDetails *ImageDetails
}
