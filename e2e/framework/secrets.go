package framework

import (
	"os"

	"github.com/onsi/ginkgo/v2"
)

// UseFileSecretsBackend forces the file backend so tests do not depend on an OS
// keyring (unavailable in CI); env is restored on cleanup.
func UseFileSecretsBackend() {
	for k, v := range map[string]string{
		"DEVSY_SECRETS_BACKEND":    "file",
		"DEVSY_SECRETS_PASSPHRASE": "e2e-passphrase",
	} {
		prev, had := os.LookupEnv(k)
		ExpectNoError(os.Setenv(k, v))
		ginkgo.DeferCleanup(func() {
			if had {
				_ = os.Setenv(k, prev)
			} else {
				_ = os.Unsetenv(k)
			}
		})
	}
}
