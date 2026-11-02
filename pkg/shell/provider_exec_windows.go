package shell

import (
	"os/exec"

	"mvdan.cc/sh/v3/interp"
)

func providerExecSignalStatus(_ *exec.ExitError) (interp.ExitStatus, bool) {
	return 0, false
}
