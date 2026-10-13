//go:build linux

package registryfixture

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if handled, code := DispatchCredentialHelper(); handled {
		os.Exit(code)
	}
	os.Exit(m.Run())
}
