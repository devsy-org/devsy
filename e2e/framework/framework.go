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
		binName += "darwin-"
	case "linux":
		binName += "linux-"
	case "windows":
		binName += "windows-"
	}

	switch runtime.GOARCH {
	case "amd64":
		binName += "amd64"
	case "arm64":
		binName += "arm64"
	}

	if runtime.GOOS == "windows" {
		binName += ".exe"
	}

	return &Framework{DevsyBinDir: path, DevsyBinName: binName}
}
