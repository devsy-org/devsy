package dependencies

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func inspectLookupFile(ctx context.Context, path string) (lookupFile, error) {
	metadata := &nativeWindowsLookup{files: make(map[uintptr]*os.File)}
	return inspectWindowsLookup(ctx, path, metadata)
}

type nativeWindowsLookup struct {
	files map[uintptr]*os.File
}

func (*nativeWindowsLookup) driveTarget(drive string) (string, error) {
	name, err := windows.UTF16PtrFromString(drive)
	if err != nil {
		return "", err
	}
	// Query the DOS-device namespace without opening the mapped filesystem.
	const bufferSize = 32768
	buffer := make([]uint16, bufferSize)
	_, err = windows.QueryDosDevice(name, &buffer[0], bufferSize)
	return windows.UTF16ToString(buffer), err
}

func (m *nativeWindowsLookup) open(parent uintptr, name string) (uintptr, error) {
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return 0, err
	}
	attributes := windows.OBJECT_ATTRIBUTES{
		RootDirectory: windows.Handle(parent), ObjectName: objectName,
		Attributes: windows.OBJ_CASE_INSENSITIVE,
	}
	attributes.Length = uint32(unsafe.Sizeof(attributes))
	var handle windows.Handle
	// FILE_OPEN_REPARSE_POINT protects only the last component. The caller
	// supplies one child at a time, anchored to a previously inspected handle.
	err = windows.NtCreateFile(
		&handle,
		windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE,
		&attributes,
		&windows.IO_STATUS_BLOCK{},
		nil,
		0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN,
		windows.FILE_OPEN_REPARSE_POINT|windows.FILE_OPEN_NO_RECALL|windows.FILE_SYNCHRONOUS_IO_NONALERT,
		0,
		0,
	)
	if err != nil {
		if status, ok := errors.AsType[windows.NTStatus](err); ok {
			err = status.Errno()
		}
		if !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, fs.ErrPermission) {
			err = errors.Join(errLookupDeferred, err)
		}
		return 0, err
	}
	key := uintptr(handle)
	m.files[key] = os.NewFile(key, name)
	return key, nil
}

func (m *nativeWindowsLookup) stat(handle uintptr) (fs.FileInfo, bool, error) {
	// File.Stat captures identity from this handle; os.SameFile will not reopen
	// a lexical path later. No path-based Lstat, Stat, EvalSymlinks or Readlink.
	info, err := m.files[handle].Stat()
	if err != nil {
		return nil, false, err
	}
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return nil, false, errLookupDeferred
	}
	return info, data.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0, nil
}

func (m *nativeWindowsLookup) close(handle uintptr) {
	_ = m.files[handle].Close()
	delete(m.files, handle)
}
