package providers

import (
	_ "embed"
)

//go:embed apple/provider.yaml
var AppleProvider string

//go:embed colima/provider.yaml
var ColimaProvider string

//go:embed docker/provider.yaml
var DockerProvider string

//go:embed kubernetes/provider.yaml
var KubernetesProvider string

//go:embed podman/provider.yaml
var PodmanProvider string

//go:embed pro/provider.yaml
var ProProvider string

// GetBuiltInProviders retrieves the built in providers.
func GetBuiltInProviders() map[string]string {
	return map[string]string{
		"apple":      AppleProvider,
		"colima":     ColimaProvider,
		"docker":     DockerProvider,
		"kubernetes": KubernetesProvider,
		"podman":     PodmanProvider,
		"pro":        ProProvider,
	}
}
