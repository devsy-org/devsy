package kubernetes

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func openImageConfig(path string) (*os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	kind, err := windows.GetFileType(windows.Handle(f.Fd()))
	if err != nil || kind != windows.FILE_TYPE_DISK {
		_ = f.Close()
		return nil, fmt.Errorf("image config file must be regular")
	}
	return f, nil
}
