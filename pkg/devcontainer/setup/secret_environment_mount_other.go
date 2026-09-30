//go:build !linux

package setup

import "fmt"

func validateSecretEnvironmentMount(string) error {
	return fmt.Errorf(
		"attached terminal secret injection requires a verifiable tmpfs runtime mount on this platform",
	)
}
