package shell

import (
	"os"
	"os/exec"
	"strings"

	"golang.org/x/sys/windows"
	"mvdan.cc/sh/v3/interp"
)

func providerPrivateDirectory(path string, info os.FileInfo) bool {
	if !info.IsDir() {
		return false
	}
	sd, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return false
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return false
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return false
	}
	sddl := sd.String()
	return strings.Count(sddl, "(") == 2 &&
		strings.Contains(sddl, "(A;OICI;GA;;;"+user.User.Sid.String()+")") &&
		(strings.Contains(sddl, "(A;OICI;GA;;;SY)") ||
			strings.Contains(sddl, "(A;OICI;GA;;;S-1-5-18)"))
}

func providerPrivateScript(info os.FileInfo) bool {
	return info.Mode().IsRegular()
}

func providerExecSignalStatus(_ *exec.ExitError) (interp.ExitStatus, bool) {
	return 0, false
}
