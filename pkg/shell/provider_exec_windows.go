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
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return false
	}
	return providerPrivateDACL(sd, user.User.Sid)
}

func providerPrivateDACL(sd *windows.SECURITY_DESCRIPTOR, user *windows.SID) bool {
	acl, ok := providerProtectedACL(sd)
	if !ok {
		return false
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return false
	}
	var grants providerACLGrants
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		ace, ok := providerACLAt(acl, i)
		if !ok || !grants.allow(ace, user, system) {
			return false
		}
	}
	return grants.private(user.Equals(system))
}

func providerProtectedACL(sd *windows.SECURITY_DESCRIPTOR) (*windows.ACL, bool) {
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return nil, false
	}
	acl, _, err := sd.DACL()
	return acl, err == nil && acl != nil
}

func providerACLAt(acl *windows.ACL, index uint32) (*windows.ACCESS_ALLOWED_ACE, bool) {
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(acl, index, &ace); err != nil {
		return nil, false
	}
	return ace, ace != nil
}

type providerACLGrants struct {
	directory   [2]uint32
	descendants [2]uint32
}

func (g *providerACLGrants) allow(ace *windows.ACCESS_ALLOWED_ACE, user, system *windows.SID) bool {
	mask, ok := providerACEMask(ace)
	if !ok {
		return false
	}
	index, ok := providerACETrustee(ace, user, system)
	if !ok {
		return false
	}
	flags := ace.Header.AceFlags
	if flags&windows.INHERIT_ONLY_ACE == 0 {
		g.directory[index] |= mask
	}
	if flags&(windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE) ==
		windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE {
		g.descendants[index] |= mask
	}
	return true
}

func providerACEMask(ace *windows.ACCESS_ALLOWED_ACE) (uint32, bool) {
	flags := ace.Header.AceFlags
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE ||
		flags & ^uint8(
			windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE|windows.INHERIT_ONLY_ACE,
		) != 0 {
		return 0, false
	}
	const genericAll = uint32(0x10000000)
	const fileAll = uint32(0x001f01ff)
	mask := uint32(ace.Mask)
	if mask & ^uint32(genericAll|fileAll) != 0 {
		return 0, false
	}
	if mask&genericAll != 0 {
		mask |= fileAll
	}
	return mask, true
}

func providerACETrustee(ace *windows.ACCESS_ALLOWED_ACE, user, system *windows.SID) (int, bool) {
	sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if sid.Equals(system) {
		return 1, true
	}
	if sid.Equals(user) {
		return 0, true
	}
	return 0, false
}

func (g providerACLGrants) private(userIsSystem bool) bool {
	const fileAll = uint32(0x001f01ff)
	if userIsSystem {
		return g.directory[1]&fileAll == fileAll && g.descendants[1]&fileAll == fileAll
	}
	return g.directory[0]&fileAll == fileAll && g.directory[1]&fileAll == fileAll &&
		g.descendants[0]&fileAll == fileAll && g.descendants[1]&fileAll == fileAll
}

func providerPrivateScript(info os.FileInfo) bool {
	return info.Mode().IsRegular()
}

func providerExecSignalStatus(_ *exec.ExitError) (interp.ExitStatus, bool) {
	return 0, false
}
