package framework

import (
	"os"

	"github.com/onsi/ginkgo/v2"
)

// SetEnvForSpec sets name to value for the current spec. The prior state is
// restored in DeferCleanup; unset and set-to-empty are kept distinct.
func SetEnvForSpec(name, value string) {
	restore, err := overrideEnv(name, &value)
	ExpectNoError(err)
	ginkgo.DeferCleanup(restore)
}

// UnsetEnvForSpec unsets name for the current spec and restores the prior state
// in DeferCleanup.
func UnsetEnvForSpec(name string) {
	restore, err := overrideEnv(name, nil)
	ExpectNoError(err)
	ginkgo.DeferCleanup(restore)
}

// overrideEnv applies value (nil means unset) and returns a restore func that
// returns name to exactly its prior state.
func overrideEnv(name string, value *string) (func(), error) {
	previous, hadPrevious := os.LookupEnv(name)
	var err error
	if value == nil {
		err = os.Unsetenv(name)
	} else {
		err = os.Setenv(name, *value)
	}
	if err != nil {
		return nil, err
	}
	return func() {
		if hadPrevious {
			_ = os.Setenv(name, previous)
			return
		}
		_ = os.Unsetenv(name)
	}, nil
}
