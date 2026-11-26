package clientimplementation

import (
	"os"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestProviderCancellationDirectoryWindowsDACL(t *testing.T) {
	dir, err := createProviderInvocationDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("remove invocation directory: %v", err)
		}
	})
	sd, err := windows.GetNamedSecurityInfo(
		dir,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("invocation directory inherits an unprotected DACL")
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sddl := sd.String()
	if !strings.Contains(sddl, user.User.Sid.String()) ||
		(!strings.Contains(sddl, ";;;SY)") && !strings.Contains(sddl, ";;;S-1-5-18)")) ||
		strings.Contains(sddl, ";;;WD)") {
		t.Fatalf(
			"invocation directory DACL does not restrict access to current user and SYSTEM: %s",
			sddl,
		)
	}
}
