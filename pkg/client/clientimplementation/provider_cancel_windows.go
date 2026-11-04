package clientimplementation

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

func createProviderInvocationDir() (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	sd, err := windows.SecurityDescriptorFromString(
		"D:P(A;OICI;GA;;;" + user.User.Sid.String() + ")(A;OICI;GA;;;SY)",
	)
	if err != nil {
		return "", err
	}
	attributes := windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: sd,
	}
	for range 10 {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", err
		}
		path, err := filepath.Abs(
			filepath.Join(os.TempDir(), "devsy-provider-invocation-"+hex.EncodeToString(random[:])),
		)
		if err != nil {
			return "", err
		}
		name, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return "", err
		}
		err = windows.CreateDirectory(name, &attributes)
		if err == nil {
			return path, nil
		}
		if !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			return "", err
		}
	}
	return "", os.ErrExist
}
