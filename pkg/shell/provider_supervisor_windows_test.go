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
