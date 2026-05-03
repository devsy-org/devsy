package config

import (
	"fmt"
	"strings"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/dockerfile"
)

const (
	DockerIDLabel           = "dev.containers.id"
	DockerfileDefaultTarget = "dev_container_auto_added_stage_label"

	DevsyContextFeatureFolder      = pkgconfig.ConfigDirName + "-internal"
	DevsyDockerlessBuildInfoFolder = "/workspaces/.dockerless"
)

func GetDockerLabelForID(id string) []string {
	return []string{DockerIDLabel + "=" + id}
}

func GetIDLabels(id string, idLabels []string) []string {
	if len(idLabels) > 0 {
		return idLabels
	}
	return GetDockerLabelForID(id)
}

func ValidateIDLabels(labels []string) error {
	for _, label := range labels {
		k, _, ok := strings.Cut(label, "=")
		if !ok || k == "" {
			return fmt.Errorf("invalid --id-label %q: must be in key=value format", label)
		}
	}
	return nil
}

type BuildInfo struct {
	ImageDetails  *ImageDetails
	ImageMetadata *ImageMetadataConfig
	ImageName     string
	PrebuildHash  string
	RegistryCache string
	Tags          []string

	Dockerless *BuildInfoDockerless
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
