package config

import "os"

const DefaultHelperImage = "busybox:latest"

// HelperImage resolves the helper image: explicit value, then
// DEVSY_HELPER_IMAGE, then DefaultHelperImage.
func HelperImage(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if env := os.Getenv(EnvHelperImage); env != "" {
		return env
	}
	return DefaultHelperImage
}
