package shell

import (
	"os"
	"os/exec"
	"unsafe"

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
	return providerPrivateDACL(sd, user.User.Sid)
}

func providerPrivateDACL(sd *windows.SECURITY_DESCRIPTOR, user *windows.SID) bool {
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return false
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil {
		return false
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return false
	}
	getAce := windows.NewLazySystemDLL("advapi32.dll").NewProc("GetAce")
	var directory, descendants [2]uint32
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ptr unsafe.Pointer
		ok, _, _ := getAce.Call(
			uintptr(unsafe.Pointer(acl)),
			uintptr(i),
			uintptr(unsafe.Pointer(&ptr)),
		)
		if ok == 0 || ptr == nil {
			return false
		}
		ace := (*windows.ACCESS_ALLOWED_ACE)(ptr)
		flags := ace.Header.AceFlags
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE ||
			flags & ^uint8(
				windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE|windows.INHERIT_ONLY_ACE,
			) != 0 {
			return false
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		index := 0
		if sid.Equals(system) {
			index = 1
		} else if !sid.Equals(user) {
			return false
		}
		mask := uint32(ace.Mask)
		const genericAll = uint32(0x10000000)
		const fileAll = uint32(0x001f01ff)
		if mask & ^uint32(genericAll|fileAll) != 0 {
			return false
		}
		if mask&genericAll != 0 {
			mask |= fileAll
		}
		if flags&windows.INHERIT_ONLY_ACE == 0 {
			directory[index] |= mask
		}
		if flags&(windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE) == windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE {
			descendants[index] |= mask
		}
	}
	const fileAll = uint32(0x001f01ff)
	if user.Equals(system) {
		return directory[1]&fileAll == fileAll && descendants[1]&fileAll == fileAll
	}
	return directory[0]&fileAll == fileAll && directory[1]&fileAll == fileAll &&
		descendants[0]&fileAll == fileAll &&
		descendants[1]&fileAll == fileAll
}

func providerPrivateScript(info os.FileInfo) bool {
	return info.Mode().IsRegular()
}

func providerExecSignalStatus(_ *exec.ExitError) (interp.ExitStatus, bool) {
	return 0, false
}
