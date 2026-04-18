package framework

import (
	"runtime"
)

type Framework struct {
	DevsyBinDir  string
	DevsyBinName string
}

func NewDefaultFramework(path string) *Framework {
	binName := "devsy-"
	switch runtime.GOOS {
	case "darwin":
		binName = binName + "darwin-"
	case "linux":
		binName = binName + "linux-"
	case "windows":
		binName = binName + "windows-"
	}

	switch runtime.GOARCH {
	case "amd64":
		binName = binName + "amd64"
	case "arm64":
		binName = binName + "arm64"
	}

	if runtime.GOOS == "windows" {
		binName = binName + ".exe"
	}

	return &Framework{DevsyBinDir: path, DevsyBinName: binName}
}
