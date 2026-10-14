//go:build linux

package e2e

import "github.com/devsy-org/devsy/e2e/framework/registryfixture"

func dispatchFixtureCredentialHelper() (bool, int) {
	return registryfixture.DispatchCredentialHelper()
}
