package docker

import (
	"os"
	"strings"

	"github.com/devsy-org/devsy/pkg/driver"
)

// selinuxLabelDisable is the security-opt value that turns off the container's
// SELinux label.
const selinuxLabelDisable = "label=disable"

// selinuxEnforcePath is a var so tests can override it.
var selinuxEnforcePath = "/sys/fs/selinux/enforce"

// addSELinuxArgs disables the container's SELinux label on enforcing hosts.
// Without it, the agent's exec into the confined container is denied
// ("fork/exec /bin/bash: permission denied"). A user-set label opt wins.
func (d *dockerDriver) addSELinuxArgs(args []string, options *driver.RunOptions) []string {
	return append(args, selinuxLabelDisableArgs(options.SecurityOpt, selinuxEnforcing())...)
}

func selinuxLabelDisableArgs(securityOpts []string, enforcing bool) []string {
	if !enforcing || userSetsSELinuxLabel(securityOpts) {
		return nil
	}
	return []string{"--security-opt", selinuxLabelDisable}
}

func userSetsSELinuxLabel(securityOpts []string) bool {
	for _, opt := range securityOpts {
		opt = strings.TrimSpace(opt)
		if opt == "label" || strings.HasPrefix(opt, "label=") || strings.HasPrefix(opt, "label:") {
			return true
		}
	}
	return false
}

func selinuxEnforcing() bool {
	data, err := os.ReadFile(selinuxEnforcePath)
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(data)) == "1"
}
