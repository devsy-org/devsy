package shell

import "fmt"

// InvalidProcessParameterHint explains a Windows ERROR_INVALID_PARAMETER returned
// while starting a provider executable.
const InvalidProcessParameterHint = "Windows rejected starting the provider executable " +
	"(ERROR_INVALID_PARAMETER) before it ran, so this is a local process-launch failure " +
	"rather than a remote host error; the cause is unconfirmed, so please report the Windows " +
	"version and whether running the executable directly from a terminal works"

// WithLaunchHint appends InvalidProcessParameterHint to a failure to start the
// provider executable and returns every other error unchanged.
func WithLaunchHint(err error) error {
	if isInvalidProcessParameter(err) {
		return fmt.Errorf("%w; %s", err, InvalidProcessParameterHint)
	}
	return err
}
