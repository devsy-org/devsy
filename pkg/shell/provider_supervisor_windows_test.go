package shell

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func providerPrivateTestDir(t *testing.T) string {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	require.NoError(t, err)
	sd, err := windows.SecurityDescriptorFromString(
		"D:P(A;OICI;GA;;;" + user.User.Sid.String() + ")(A;OICI;GA;;;SY)")
	require.NoError(t, err)
	attributes := windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: sd,
	}
	path := filepath.Join(t.TempDir(), "private")
	name, err := windows.UTF16PtrFromString(path)
	require.NoError(t, err)
	require.NoError(t, windows.CreateDirectory(name, &attributes))
	return path
}

func providerChildReaped(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}
	if err := process.Kill(); err == nil {
		return errors.New("owned child still exists after acknowledgement")
	}
	return nil
}

func TestProviderPrivateDACL(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	require.NoError(t, err)
	sid := user.User.Sid.String()
	cases := []struct {
		name string
		sddl string
		want bool
	}{
		{"creation", "D:P(A;OICI;GA;;;" + sid + ")(A;OICI;GA;;;SY)", true},
		{
			"normalized",
			"D:P(A;;FA;;;" + sid + ")(A;OICIIO;GA;;;" + sid + ")(A;;FA;;;SY)(A;OICIIO;GA;;;SY)",
			true,
		},
		{"unprotected", "D:(A;OICI;GA;;;" + sid + ")(A;OICI;GA;;;SY)", false},
		{"world", "D:P(A;OICI;GA;;;" + sid + ")(A;OICI;GA;;;SY)(A;OICI;GR;;;WD)", false},
		{"missing descendants", "D:P(A;;FA;;;" + sid + ")(A;OICI;GA;;;SY)", false},
		{"insufficient", "D:P(A;OICI;GR;;;" + sid + ")(A;OICI;GA;;;SY)", false},
		{"no propagation", "D:P(A;OICINP;GA;;;" + sid + ")(A;OICI;GA;;;SY)", false},
		{"deny", "D:P(D;;GA;;;WD)(A;OICI;GA;;;" + sid + ")(A;OICI;GA;;;SY)", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sd, err := windows.SecurityDescriptorFromString(tc.sddl)
			require.NoError(t, err)
			require.Equal(t, tc.want, providerPrivateDACL(sd, user.User.Sid))
		})
	}
}

func TestProviderPrivateDACLWhenCurrentUserIsSystem(t *testing.T) {
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	require.NoError(t, err)
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;GA;;;SY)(A;OICI;GA;;;SY)")
	require.NoError(t, err)
	require.True(t, providerPrivateDACL(sd, system))
}

func TestProviderPrivateDirectoryCreated(t *testing.T) {
	path := providerPrivateTestDir(t)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.True(t, providerPrivateDirectory(path, info))
}
