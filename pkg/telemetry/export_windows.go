package telemetry

import (
	"errors"
	"io"
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func appendMetricsFile(path string, data []byte) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return err
	}
	attributes := windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: sd,
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(
		name,
		windows.FILE_APPEND_DATA|windows.READ_CONTROL|windows.SYNCHRONIZE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		&attributes,
		windows.OPEN_ALWAYS,
		windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(handle), "metrics")
	defer func() { _ = file.Close() }()
	if err := validateMetricsFile(handle, user.User.Sid); err != nil {
		return err
	}
	var overlapped windows.Overlapped
	if err := lockMetricsFile(handle, &overlapped); err != nil {
		return err
	}
	defer func() { _ = windows.UnlockFileEx(handle, 0, 1, 0, &overlapped) }()
	return writeMetricsFile(file, data)
}

func writeMetricsFile(file *os.File, data []byte) error {
	n, err := file.Write(data)
	if err != nil {
		return err
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	return nil
}

func validateMetricsFile(handle windows.Handle, sid *windows.SID) error {
	kind, err := windows.GetFileType(handle)
	if err != nil || kind != windows.FILE_TYPE_DISK {
		return errors.New("unsafe metrics file")
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		return errors.New("unsafe metrics file")
	}
	return validateMetricsSecurity(handle, sid)
}

func validateMetricsSecurity(handle windows.Handle, sid *windows.SID) error {
	security, err := windows.GetSecurityInfo(
		handle,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return err
	}
	if security == nil || !security.IsValid() || sid == nil || !sid.IsValid() {
		return errors.New("unsafe metrics security")
	}
	descriptor := unsafe.Slice((*byte)(unsafe.Pointer(security)), int(security.Length()))
	user := unsafe.Slice((*byte)(unsafe.Pointer(sid)), sid.Len())
	if !validMetricsSecurity(descriptor, user) {
		return errors.New("unsafe metrics security")
	}
	return nil
}

func lockMetricsFile(handle windows.Handle, overlapped *windows.Overlapped) error {
	deadline := time.Now().Add(250 * time.Millisecond)
	for {
		err := windows.LockFileEx(
			handle,
			windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
			0,
			1,
			0,
			overlapped,
		)
		if err == nil {
			return nil
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
}
