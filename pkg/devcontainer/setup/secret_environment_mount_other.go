//go:build !linux

package setup

import "fmt"

func validateSecretEnvironmentMount(string) error {
	return fmt.Errorf(
		"attached terminal secret injection requires a verifiable tmpfs runtime mount on this platform",
	)
}

func validateSecretFileMount(string) error {
	return fmt.Errorf(
		"file-mounted secret injection requires a verifiable tmpfs runtime mount on this platform",
	)
}
